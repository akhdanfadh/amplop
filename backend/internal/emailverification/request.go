package emailverification

import (
	"context"
	"errors"
	"time"

	"inbok/internal/attemptlimit"
	"inbok/internal/systememail"
)

// Product policy: enough for a lost or slow email, too few to flood an inbox.
var (
	shortLimit = attemptlimit.Limit{Window: 15 * time.Minute, Max: 3}
	longLimit  = attemptlimit.Limit{Window: 24 * time.Hour, Max: 10}
)

const (
	messagePurpose = "email verification"
	messageSubject = "Verify your email address for inbok"
)

var (
	// ErrAlreadyVerified means the user's address is already verified.
	ErrAlreadyVerified = errors.New("email address already verified")
	// ErrThrottled means the user has asked for too many links recently.
	ErrThrottled = errors.New("email verification links throttled")
)

// RequestState is what [Storage.RequestEmailVerification] reads under the
// user's lock.
type RequestState struct {
	Email          string
	Verified       bool
	MailConfigured bool        // system email settings are saved
	LinksCreatedAt []time.Time // creation times of the user's saved links
}

// RequestChange is the [Link] to save and the cutoff for deleting the user's
// expired links.
type RequestChange struct {
	Link      Link
	ExpiredBy time.Time
}

// Request saves a verification link for userID's current address and emails
// it there. It returns the address. It returns [ErrAlreadyVerified],
// [ErrThrottled], or [systememail.ErrNotConfigured] without saving or
// sending, and the error from [systememail.Mailer.Send] after saving.
func (s *Service) Request(ctx context.Context, userID int64) (string, error) {
	secret, hash := newSecret()
	var link Link
	// Saving before sending reserves the link, so it counts toward the
	// throttle even when the send fails after the server accepted it.
	err := s.storage.RequestEmailVerification(ctx, userID, func(state RequestState) (RequestChange, error) {
		// Read under the user's lock, so a request that waited counts from now.
		now := s.now().UTC()
		switch {
		case state.Verified:
			return RequestChange{}, ErrAlreadyVerified
		case attemptlimit.Wait(state.LinksCreatedAt, now, shortLimit, longLimit) > 0:
			return RequestChange{}, ErrThrottled
		case !state.MailConfigured:
			return RequestChange{}, systememail.ErrNotConfigured
		}
		link = Link{TokenHash: hash, UserID: userID, Address: state.Email, CreatedAt: now, ExpiresAt: now.Add(linkLifetime)}
		// Expired links have also left the throttle's longest window, because
		// linkLifetime is at least longLimit.Window, so deleting them loses no count.
		return RequestChange{Link: link, ExpiredBy: now}, nil
	})
	if err != nil {
		return "", err
	}

	if _, err := s.mailer.Send(ctx, systememail.Outgoing{
		Purpose: messagePurpose,
		To:      link.Address,
		Subject: messageSubject,
		Body:    s.messageBody(secret),
		Private: true,
	}); err != nil {
		return "", err
	}
	return link.Address, nil
}

// messageBody returns the plain-text email that carries secret in its link.
func (s *Service) messageBody(secret string) string {
	link := *s.publicURL
	link.Path = "/verify-email"
	// The secret goes in the fragment, which browsers never send to a
	// server, so it stays out of request logs, proxies, and Referer headers.
	link.Fragment = secret
	return "Someone asked inbok at " + s.publicURL.String() + " to verify this email address.\n\n" +
		"To verify it, open this link and select Verify email:\n\n" +
		link.String() + "\n\n" +
		"The link expires in 24 hours. If you didn't ask for this, you can ignore this email.\n"
}
