package emailverification

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"
)

// ErrLinkInvalid means a link is malformed, unknown, used, expired, or was
// sent to an address the user no longer has.
var ErrLinkInvalid = errors.New("email verification link is not valid")

// RedeemState is what [Storage.RedeemEmailVerification] reads under the
// user's lock.
type RedeemState struct {
	Link     Link
	Email    string // the user's current email
	Verified bool
}

// RedeemChange is the verified time to save, nil to keep the user's current
// one.
type RedeemChange struct {
	VerifiedAt *time.Time
}

// Redeem verifies the address the link with secret was sent to and deletes
// all of its user's links. A link for an already verified user succeeds and
// keeps their verified time. It returns [ErrLinkInvalid] for a link that is
// malformed, unknown, used, expired, or sent to an address the user no longer
// has.
//
// NOTE: Redeem has no throttle. The secret has 256 random bits, so guessing
// one is hopeless however fast the guesses come.
func (s *Service) Redeem(ctx context.Context, secret string) error {
	hash, ok := hashSecret(secret)
	if !ok {
		return ErrLinkInvalid
	}
	return s.storage.RedeemEmailVerification(ctx, hash, func(state RedeemState) (RedeemChange, error) {
		now := s.now().UTC()
		// The link saved an exact copy of the email, and inbok keeps the local
		// part's case, so any difference may be another mailbox.
		if !state.Link.ExpiresAt.After(now) || state.Link.Address != state.Email {
			return RedeemChange{}, ErrLinkInvalid
		}
		if state.Verified {
			return RedeemChange{}, nil
		}
		return RedeemChange{VerifiedAt: &now}, nil
	})
}

// hashSecret returns the SHA-256 hash of the bytes secret encodes. It
// reports false when secret is not exactly [secretSize] bytes in unpadded
// URL-safe base64.
func hashSecret(secret string) ([sha256.Size]byte, bool) {
	if len(secret) != base64.RawURLEncoding.EncodedLen(secretSize) {
		return [sha256.Size]byte{}, false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(secret)
	if err != nil {
		return [sha256.Size]byte{}, false
	}
	// Even strict base64 ignores line breaks, so text of the right length can
	// decode short.
	if len(raw) != secretSize {
		return [sha256.Size]byte{}, false
	}
	return sha256.Sum256(raw), true
}
