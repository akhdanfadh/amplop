// Package passwordreset lets a user who forgot their password choose a new
// one by following a link inbok sends to their verified email.
package passwordreset

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"inbok/internal/systememail"
)

const linkLifetime = time.Hour

// resetPath is the web app's page that completes a reset; links carry their
// secret in its fragment.
const resetPath = "/reset-password"

// ErrLinkInvalid means a reset link is malformed, unknown, used, expired,
// sent to an email the user no longer has, or owned by a deactivated user.
var ErrLinkInvalid = errors.New("password reset link is not valid")

// Link is a saved reset link without its secret.
type Link struct {
	TokenHash [sha256.Size]byte
	UserID    int64
	Email     string // the user's email when the link was sent
	CreatedAt time.Time
	ExpiresAt time.Time
}

// mailer sends one message with the system email settings; a
// [systememail.Mailer] does.
type mailer interface {
	Send(ctx context.Context, outgoing systememail.Outgoing) (int64, error)
}
