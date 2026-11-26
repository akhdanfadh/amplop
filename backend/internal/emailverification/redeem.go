package emailverification

import (
	"context"
	"errors"
	"time"

	"amplop/internal/randomsecret"
)

// ErrLinkInvalid means a link is malformed, unknown, used, expired, sent to
// an address the user no longer has, or owned by a deactivated user.
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
// malformed, unknown, used, expired, sent to an address the user no longer
// has, or owned by a deactivated user.
//
// DESIGN: Redeem has no throttle. The secret has 256 random bits, so guessing
// one is hopeless however fast the guesses come.
func (s *Service) Redeem(ctx context.Context, secret string) error {
	// 1. Refuse a malformed secret before any storage.
	hash, err := randomsecret.Hash(secret)
	if err != nil {
		return ErrLinkInvalid
	}

	// 2. Under the user's lock, check the link and save the verified time.
	return s.storage.RedeemEmailVerification(ctx, hash, func(state RedeemState) (RedeemChange, error) {
		now := s.now().UTC()
		// The link saved an exact copy of the email, which is already in
		// emailaddress.Parse's form, so any difference is another address.
		if !state.Link.ExpiresAt.After(now) || state.Link.Email != state.Email {
			return RedeemChange{}, ErrLinkInvalid
		}
		if state.Verified {
			return RedeemChange{}, nil
		}
		return RedeemChange{VerifiedAt: &now}, nil
	})
}
