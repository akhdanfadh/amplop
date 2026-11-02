package registration

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/netip"
	"net/url"
	"time"

	"inbok/internal/attemptlimit"
	"inbok/internal/emailaddress"
	"inbok/internal/randomsecret"
	"inbok/internal/systememail"
)

const linkLifetime = 24 * time.Hour

// Product policy (ADR028). The email limits keep one inbox from being
// flooded; the client limit slows one visitor; the instance limit caps how
// much mail strangers can make inbok send, protecting its sender's
// reputation.
var (
	emailShortLimit = attemptlimit.Limit{Window: time.Hour, Max: 3}
	emailLongLimit  = attemptlimit.Limit{Window: 24 * time.Hour, Max: 5}
	clientLimit     = attemptlimit.Limit{Window: 15 * time.Minute, Max: 10}
	instanceLimit   = attemptlimit.Limit{Window: time.Hour, Max: 50}
)

// attemptRetention is the longest window, so dropping older attempts loses
// no count.
const attemptRetention = 24 * time.Hour

const (
	linkPurpose   = "registration link"
	linkSubject   = "Create your inbok account"
	noticePurpose = "registration notice"
	noticeSubject = "Your email address already has an inbok account"
)

var (
	// ErrClosed means visitors cannot register now: registration is closed,
	// or paused until system email passes a test.
	ErrClosed = errors.New("registration is not open")
	// ErrThrottled means the client, or everyone together, has asked for too
	// many registration emails recently.
	ErrThrottled = errors.New("registration requests throttled")

	errClientAddr = errors.New("invalid client address")
)

// Outcome is what an accepted registration request sent.
type Outcome string

const (
	OutcomeLink    Outcome = "link"    // a registration link, to an email without an account
	OutcomeNotice  Outcome = "notice"  // a notice, to an email that has an account
	OutcomeSkipped Outcome = "skipped" // nothing: the email reached its limit
)

// Attempt is one accepted registration request, counted toward the limits.
type Attempt struct {
	Email      string
	ClientAddr string // the client's IP address, IPv4 unmapped from IPv6
	Outcome    Outcome
	CreatedAt  time.Time
}

// Link is a saved registration link without its secret.
type Link struct {
	TokenHash [sha256.Size]byte
	Email     string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// RequestState is what [requestStorage.RequestRegistration] reads under its
// lock.
type RequestState struct {
	Registration  Record
	AccountExists bool      // an account has the requested email
	Attempts      []Attempt // every attempt kept, from every client
}

// RequestChange is the attempt to record, the link to save (nil unless the
// attempt's outcome is [OutcomeLink]), and the cutoff for deleting expired
// links.
type RequestChange struct {
	Attempt   Attempt
	Link      *Link
	ExpiredBy time.Time
}

// requestStorage records registration requests.
type requestStorage interface {
	// ReadRegistration returns the saved state, without a lock.
	ReadRegistration(ctx context.Context) (Record, error)

	// RequestRegistration serializes every registration request, deletes
	// attempts created at or before since, reads the [RequestState] for
	// email, and calls create. If create succeeds, it deletes registration
	// links and email verification links expiring at or before
	// [RequestChange.ExpiredBy], records the attempt, and saves the link, all
	// in one transaction. An error from create rolls back and is returned
	// unchanged.
	RequestRegistration(ctx context.Context, email string, since time.Time, create func(RequestState) (RequestChange, error)) error
}

type mailer interface {
	Send(ctx context.Context, outgoing systememail.Outgoing) (int64, error)
}

// Requests answers visitors' registration requests by email.
type Requests struct {
	storage   requestStorage
	mailer    mailer
	publicURL *url.URL
	now       func() time.Time
}

// NewRequests returns Requests given storage, the [systememail.Mailer] that
// sends, the public URL links start with, and a clock.
func NewRequests(storage requestStorage, mailer mailer, publicURL *url.URL, now func() time.Time) *Requests {
	return &Requests{storage: storage, mailer: mailer, publicURL: publicURL, now: now}
}

// Send answers a request to register email from client. A new email gets a
// registration link; an email with an account gets a notice instead; an
// email at its limit gets nothing. When every send succeeds, all three
// return nil, so the caller cannot tell them apart. It returns
// [emailaddress.ErrInvalid] for an invalid email, [ErrClosed] unless
// registration is open, and an [*attemptlimit.Error] wrapping [ErrThrottled]
// for a client or instance over its limit, all without recording or sending
// anything, and the error from [systememail.Mailer.Send] after recording.
//
// NOTE: Two differences remain, and ADR028 accepts both. A skipped request
// returns sooner than one that sends. While mail fails, a skipped request
// still returns nil where the others return the send's error. Either hints
// that an email was asked for recently, never that it has an account.
func (r *Requests) Send(ctx context.Context, email string, client netip.Addr) error {
	email, err := emailaddress.Parse(email)
	if err != nil {
		return err
	}
	if !client.IsValid() {
		return errClientAddr
	}
	clientAddr := client.Unmap().WithZone("").String()

	// Closed is the default, so refuse it before the lock every request
	// shares: queued requests would hold connections other routes need.
	record, err := r.storage.ReadRegistration(ctx)
	if err != nil {
		return err
	}
	if statusOf(record) != StatusOpen {
		return ErrClosed
	}

	secret, hash := randomsecret.New()

	var change RequestChange
	since := r.now().UTC().Add(-attemptRetention)
	err = r.storage.RequestRegistration(ctx, email, since, func(state RequestState) (RequestChange, error) {
		// Read under the lock, so a request that waited counts from now.
		now := r.now().UTC()
		// An administrator may have closed it while this request waited.
		if statusOf(state.Registration) != StatusOpen {
			return RequestChange{}, ErrClosed
		}

		var fromClient, fromAll, toEmail []time.Time
		for _, attempt := range state.Attempts {
			fromAll = append(fromAll, attempt.CreatedAt)
			if attempt.ClientAddr == clientAddr {
				fromClient = append(fromClient, attempt.CreatedAt)
			}
			// Skipped requests sent nothing. Counting them would let anyone keep an
			// email locked out for as long as they kept asking.
			if attempt.Email == email && attempt.Outcome != OutcomeSkipped {
				toEmail = append(toEmail, attempt.CreatedAt)
			}
		}
		// These limits come before the email's own, so a refusal never
		// depends on the email asked for.
		if wait := max(attemptlimit.Wait(fromClient, now, clientLimit), attemptlimit.Wait(fromAll, now, instanceLimit)); wait > 0 {
			return RequestChange{}, &attemptlimit.Error{Err: ErrThrottled, Wait: wait}
		}

		change = RequestChange{
			Attempt:   Attempt{Email: email, ClientAddr: clientAddr, Outcome: OutcomeLink, CreatedAt: now},
			ExpiredBy: now,
		}
		switch {
		case attemptlimit.Wait(toEmail, now, emailShortLimit, emailLongLimit) > 0:
			change.Attempt.Outcome = OutcomeSkipped
		case state.AccountExists:
			change.Attempt.Outcome = OutcomeNotice
		default:
			change.Link = &Link{TokenHash: hash, Email: email, CreatedAt: now, ExpiresAt: now.Add(linkLifetime)}
		}
		return change, nil
	})
	if err != nil {
		return err
	}

	switch change.Attempt.Outcome {
	case OutcomeSkipped:
		return nil
	case OutcomeNotice:
		_, err = r.mailer.Send(ctx, systememail.Outgoing{Purpose: noticePurpose, To: email, Subject: noticeSubject, Body: r.noticeBody()})
		return err
	}
	// A failed send keeps its link: a timed-out message may still arrive, and
	// no one else holds the secret.
	_, err = r.mailer.Send(ctx, systememail.Outgoing{Purpose: linkPurpose, To: email, Subject: linkSubject, Body: r.linkBody(secret), Private: true})
	return err
}

// linkBody returns the plain-text email that carries secret in its link.
func (r *Requests) linkBody(secret string) string {
	link := *r.publicURL
	link.Path = "/register/complete"
	// The secret goes in the fragment, which browsers never send to a
	// server, so it stays out of request logs, proxies, and Referer headers.
	link.Fragment = secret
	return "Someone asked to create an account on inbok at " + r.publicURL.String() + " with this email address.\n\n" +
		"To create it, open this link and choose a password:\n\n" +
		link.String() + "\n\n" +
		"The link expires in 24 hours. If you didn't ask for this, you can ignore this email. No account is created unless the link is used.\n"
}

// noticeBody returns the plain-text email telling an account's owner that
// someone tried to register their email.
func (r *Requests) noticeBody() string {
	signIn := *r.publicURL
	signIn.Path = "/sign-in"
	return "Someone asked to create an account on inbok at " + r.publicURL.String() + " with this email address. " +
		"You already have an account there, so no new one was created.\n\n" +
		"To use your account, sign in here:\n\n" +
		signIn.String() + "\n\n" +
		"If you didn't ask for this, you can ignore this email. Your account has not changed.\n"
}
