package passwordreset

import (
	"context"
	"errors"
	"net/netip"
	"net/url"
	"time"

	"inbok/internal/account"
	"inbok/internal/attemptlimit"
	"inbok/internal/clientprefix"
	"inbok/internal/emailaddress"
	"inbok/internal/randomsecret"
	"inbok/internal/systememail"
)

// Product policy (ADR030), from registration's (ADR028). The email limits
// keep one inbox from being flooded; the client limit slows one visitor.
var (
	emailShortLimit = attemptlimit.Limit{Window: time.Hour, Max: 3}
	emailLongLimit  = attemptlimit.Limit{Window: 24 * time.Hour, Max: 5}
	clientLimit     = attemptlimit.Limit{Window: 15 * time.Minute, Max: 10}
)

// attemptRetention is the longest window, so dropping older attempts loses
// no count.
const attemptRetention = 24 * time.Hour

const (
	linkPurpose = "password reset link"
	linkSubject = "Reset your inbok password"
)

var (
	// ErrThrottled means the client has asked for too many reset links
	// recently.
	ErrThrottled = errors.New("password reset requests throttled")

	errClientAddr = errors.New("invalid client address")
)

// Account is the account a reset request names, as read under its lock.
type Account struct {
	User        account.User
	Verified    bool // the account proved control of its email
	Deactivated bool
}

// LinkState is what [requestStorage.CreatePasswordResetLink] reads under the
// account's lock.
type LinkState struct {
	Account   *Account    // nil: no account has the email
	LinksSent []time.Time // when links were saved for the account, within the longest window
}

// LinkChange is the link to save, nil to send none, and the cutoff for
// deleting every user's expired links.
type LinkChange struct {
	Link      *Link
	ExpiredBy time.Time
}

// requestStorage records reset requests and saves their links.
type requestStorage interface {
	// RecordPasswordResetRequest serializes the requests of client, an
	// IPv4 address or an IPv6 /64 written as a prefix, deletes every
	// attempt whose request and link times are both at or before since, and
	// calls check with the times of client's requests. If check succeeds, it records an attempt from
	// client at the time check returns and returns the attempt's ID, all in
	// one transaction. An error from check rolls back and is returned
	// unchanged.
	RecordPasswordResetRequest(ctx context.Context, client string, since time.Time, check func([]time.Time) (time.Time, error)) (int64, error)

	// CreatePasswordResetLink locks the account with email, if any, reads
	// the [LinkState] with the times links were saved for it after since,
	// and calls create. If create succeeds, it deletes every user's links
	// expiring at or before [LinkChange.ExpiredBy] and, when
	// [LinkChange.Link] is set, saves it for the locked account and marks
	// the attempt with attemptID with the account and the link's creation
	// time, all in one transaction. An error from create rolls
	// back and is returned unchanged.
	CreatePasswordResetLink(ctx context.Context, attemptID int64, email string, since time.Time, create func(LinkState) (LinkChange, error)) error
}

// Requests answers requests for reset links.
type Requests struct {
	storage    requestStorage
	mailer     mailer
	publicURL  *url.URL
	background *Background
	now        func() time.Time
}

// NewRequests returns Requests given storage, the [systememail.Mailer] that
// sends, the public URL links start with, the [Background] that sends after
// the answer, and a clock.
func NewRequests(storage requestStorage, mailer mailer, publicURL *url.URL, background *Background, now func() time.Time) *Requests {
	return &Requests{storage: storage, mailer: mailer, publicURL: publicURL, background: background, now: now}
}

// Request accepts a request from client for a reset link to email and
// returns nil, the same for every address. Once it records the attempt, it
// starts background work, which it does not wait for, that sends a link if
// email belongs to an active account with a verified email under its
// limits, and nothing otherwise. It returns [emailaddress.ErrInvalid] for an
// invalid email, and an [*attemptlimit.Error] wrapping [ErrThrottled] for a
// client over its limit, both without recording anything, and storage's
// error if recording fails.
//
// DESIGN: before it returns, Request reads nothing about the account, so its
// answer and its timing are the same for every address; OWASP's Forgot
// Password Cheat Sheet asks for that. The email limits are silent for the
// same reason: past them, nothing is sent and the answer does not change.
func (r *Requests) Request(ctx context.Context, email string, client netip.Addr) error {
	// 1. Refuse what needs no storage: a malformed email or no client.
	email, err := emailaddress.Parse(email)
	if err != nil {
		return err
	}
	if !client.IsValid() {
		return errClientAddr
	}

	// 2. Under the client's lock, check its limit and record the attempt.
	// Nothing here reads the account.
	since := r.now().UTC().Add(-attemptRetention)
	attemptID, err := r.storage.RecordPasswordResetRequest(ctx, clientprefix.Of(client).String(), since, func(times []time.Time) (time.Time, error) {
		// Read under the lock, so a request that waited counts from now.
		now := r.now().UTC()
		if wait := attemptlimit.Wait(times, now, clientLimit); wait > 0 {
			return time.Time{}, &attemptlimit.Error{Err: ErrThrottled, Wait: wait}
		}
		return now, nil
	})
	if err != nil {
		return err
	}

	// 3. Start the rest without waiting, so the answer is the same for
	// every address.
	r.background.Go(ctx, linkPurpose, func(ctx context.Context) error {
		return r.send(ctx, attemptID, email)
	})
	return nil
}

// send saves and sends a link to email for the request with attemptID when
// email's account may reset, and returns the send's error.
func (r *Requests) send(ctx context.Context, attemptID int64, email string) error {
	// 1. Make the secret before storage takes the account's lock.
	secret, hash := randomsecret.New()

	var to string
	since := r.now().UTC().Add(-attemptRetention)
	err := r.storage.CreatePasswordResetLink(ctx, attemptID, email, since, func(state LinkState) (LinkChange, error) {
		// 2. Under the account's lock, send nothing unless the account may
		// reset and is under its limits. Every request cleans up either way.
		now := r.now().UTC()
		change := LinkChange{ExpiredBy: now}
		if !mayReset(state.Account) || attemptlimit.Wait(state.LinksSent, now, emailShortLimit, emailLongLimit) > 0 {
			return change, nil
		}
		// 3. Save a link for the stored email, already in
		// emailaddress.Parse's form.
		to = state.Account.User.Email
		change.Link = &Link{
			TokenHash: hash, UserID: state.Account.User.ID, Email: to,
			CreatedAt: now, ExpiresAt: now.Add(linkLifetime),
		}
		return change, nil
	})
	if err != nil || to == "" {
		return err
	}

	// 4. Mail the link once it is saved. A failed send keeps its link: a
	// timed-out message may still arrive, and no one else holds the secret.
	_, err = r.mailer.Send(ctx, systememail.Outgoing{
		Purpose: linkPurpose, To: to, Subject: linkSubject, Body: r.linkBody(secret), Private: true,
	})
	return err
}

// mayReset reports whether account exists, is active, and has a verified
// email.
func mayReset(account *Account) bool {
	return account != nil && account.Verified && !account.Deactivated
}

// linkBody returns the plain-text email that carries secret in its link.
func (r *Requests) linkBody(secret string) string {
	link := *r.publicURL
	link.Path = resetPath
	// The secret goes in the fragment, which browsers never send to a
	// server, so it stays out of request logs, proxies, and Referer headers.
	link.Fragment = secret
	return "Someone asked to reset the password of your inbok account at " + r.publicURL.String() + ".\n\n" +
		"To choose a new password, open this link:\n\n" +
		link.String() + "\n\n" +
		"The link expires in 1 hour. If you didn't ask for this, you can ignore this email. Your password stays the same unless the link is used.\n"
}
