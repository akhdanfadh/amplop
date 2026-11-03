// Package passwordreset lets a user who forgot their password choose a new
// one by following a link inbok sends to their verified email.
package passwordreset

import (
	"crypto/sha256"
	"errors"
	"time"
)

const linkLifetime = time.Hour

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
