package passwordreset

import (
	"context"
	"errors"
	"net/url"
	"time"

	"inbok/internal/account"
	"inbok/internal/attemptlimit"
	"inbok/internal/randomsecret"
	"inbok/internal/systememail"
)

const (
	linkPurpose = "password reset link"
	linkSubject = "Reset your inbok password"
)

// ErrLinkNotSent means a reset link's send failed in a way worth retrying.
// It carries only where the send failed, never the server's reply.
var ErrLinkNotSent = errors.New("password reset link not sent")

// Account is the account a reset request names, as read under its lock.
type Account struct {
	User        account.User
	Verified    bool // the account proved control of its email
	Deactivated bool
}

// LinkState is what [linkStorage.CreatePasswordResetLink] reads under the
// account's lock.
type LinkState struct {
	Account   *Account    // nil: no account has the email
	LinksSent []time.Time // when links were saved for the account, within the longest window
}

// LinkChange is the link to save, nil to send none.
type LinkChange struct {
	Link *Link
}

// linkStorage saves reset links.
type linkStorage interface {
	// CreatePasswordResetLink locks the account with userID, if any, reads
	// the [LinkState] with the times links were saved for it after since,
	// and calls create. If create sets [LinkChange.Link], it saves it for
	// the locked account and counts it: it marks the attempt with attemptID
	// with the account and the link's creation time, or, when that attempt
	// already counts a link or is gone, saves a row of its own for the
	// account, all in one transaction. An error from create rolls back and
	// is returned unchanged.
	CreatePasswordResetLink(ctx context.Context, attemptID, userID int64, since time.Time, create func(LinkState) (LinkChange, error)) error
}

// Links sends the reset links that requests asked for; the reset link job
// calls Send.
type Links struct {
	storage   linkStorage
	mailer    mailer
	publicURL *url.URL
	now       func() time.Time
}

// NewLinks returns Links given storage, the [systememail.Mailer] that sends,
// the public URL links start with, and a clock.
func NewLinks(storage linkStorage, mailer mailer, publicURL *url.URL, now func() time.Time) *Links {
	return &Links{storage: storage, mailer: mailer, publicURL: publicURL, now: now}
}

// Send saves and mails a link for the request with attemptID to the account
// with userID, 0 for none, when it exists, is active, has a verified email,
// and is under its limits, and returns [OutcomeSent]; otherwise it saves and
// sends nothing and returns [OutcomeNotEligible]. A send that retrying cannot fix
// returns its outcome, e.g. [OutcomeNotConfigured]. It returns storage's error,
// or an error wrapping [ErrLinkNotSent] for a send worth retrying, such as
// an unconfirmed one. Each call saves a new link, which counts toward the
// account's limits, so a retry may deliver a second working link.
func (l *Links) Send(ctx context.Context, attemptID, userID int64) (Outcome, error) {
	if userID == 0 {
		return OutcomeNotEligible, nil
	}

	// 1. Make the secret before storage takes the account's lock.
	secret, hash := randomsecret.New()

	var to string
	since := l.now().UTC().Add(-attemptRetention)
	err := l.storage.CreatePasswordResetLink(ctx, attemptID, userID, since, func(state LinkState) (LinkChange, error) {
		// 2. Under the account's lock, send nothing unless the account may
		// reset and is under its limits.
		now := l.now().UTC()
		var change LinkChange
		if !mayReset(state.Account) || attemptlimit.Wait(state.LinksSent, now, emailShortLimit, emailLongLimit) > 0 {
			return change, nil
		}

		// 3. Save a link for the stored email.
		to = state.Account.User.Email
		change.Link = &Link{
			TokenHash: hash, UserID: state.Account.User.ID, Email: to,
			CreatedAt: now, ExpiresAt: now.Add(linkLifetime),
		}
		return change, nil
	})
	if err != nil {
		return "", err
	}
	if to == "" {
		return OutcomeNotEligible, nil
	}

	// 4. Mail the link once it is saved, and turn a failure retrying cannot
	// fix into an outcome. A failed send keeps its link: a timed-out
	// message may still arrive, and no one else holds the secret.
	_, err = l.mailer.Send(ctx, systememail.Outgoing{
		Purpose: linkPurpose, To: to, Subject: linkSubject, Body: l.linkBody(secret), Private: true,
	})
	return sendOutcome(err, ErrLinkNotSent)
}

// mayReset reports whether account exists, is active, and has a verified
// email.
func mayReset(account *Account) bool {
	return account != nil && account.Verified && !account.Deactivated
}

// linkBody returns the plain-text email that carries secret in its link.
func (l *Links) linkBody(secret string) string {
	link := *l.publicURL
	link.Path = resetPath
	// The secret goes in the fragment, which browsers never send to a
	// server, so it stays out of request logs, proxies, and Referer headers.
	link.Fragment = secret
	return "Someone asked to reset the password of your inbok account at " + l.publicURL.String() + ".\n\n" +
		"To choose a new password, open this link:\n\n" +
		link.String() + "\n\n" +
		"The link expires in 1 hour. If you didn't ask for this, you can ignore this email. Your password stays the same unless the link is used.\n"
}
