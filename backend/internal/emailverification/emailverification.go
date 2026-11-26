// Package emailverification lets a user prove control of their email
// address by following a link amplop sends to it.
package emailverification

import (
	"context"
	"crypto/sha256"
	"net/url"
	"time"

	"amplop/internal/systememail"
)

const linkLifetime = 24 * time.Hour

// Link is a saved verification link without its secret.
type Link struct {
	TokenHash [sha256.Size]byte
	UserID    int64
	Email     string // the user's email when the link was sent
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Storage keeps verification links and the users' verified state.
type Storage interface {
	// RequestEmailVerification locks the user, reads their [RequestState], and
	// calls create. If create succeeds, it deletes the user's links expiring at
	// or before [RequestChange.ExpiredBy] and saves [RequestChange.Link] as the
	// user's, all in one transaction. A missing or deactivated user returns
	// account.ErrUserNotFound. An error from create rolls back and is
	// returned unchanged.
	RequestEmailVerification(ctx context.Context, userID int64, create func(RequestState) (RequestChange, error)) error

	// RedeemEmailVerification finds the link with hash, locks its user, reads
	// their [RedeemState], and calls redeem. If redeem succeeds, it deletes all
	// the user's links and, when [RedeemChange.VerifiedAt] is set, saves it
	// and records an email-verified event with the user as target and no
	// actor, all in one transaction. A link that is missing, deleted while
	// waiting for the lock, or owned by a deactivated user returns
	// [ErrLinkInvalid]. An error from redeem
	// rolls back and is returned unchanged.
	RedeemEmailVerification(ctx context.Context, hash [sha256.Size]byte, redeem func(RedeemState) (RedeemChange, error)) error
}

type mailer interface {
	Send(ctx context.Context, outgoing systememail.Outgoing) (int64, error)
}

// Service sends verification links and redeems them.
type Service struct {
	storage   Storage
	mailer    mailer
	publicURL *url.URL
	now       func() time.Time
}

// NewService supplies storage, the [systememail.Mailer] that sends with the
// system email settings, the public URL links start with, and a clock.
func NewService(storage Storage, mailer mailer, publicURL *url.URL, now func() time.Time) *Service {
	return &Service{storage: storage, mailer: mailer, publicURL: publicURL, now: now}
}
