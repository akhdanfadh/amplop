package passwordreset

import (
	"context"
	"crypto/sha256"
	"time"

	"inbok/internal/account"
	"inbok/internal/passwordhash"
	"inbok/internal/randomsecret"
)

// CompleteState is what [completeStorage] reads for one reset link.
type CompleteState struct {
	Link        *Link        // nil: no link has the secret's hash
	User        account.User // the link's user; zero without a link
	Deactivated bool         // the link's user is deactivated
}

// CompleteChange is the new password's hash.
type CompleteChange struct {
	PasswordHash string
}

// completeStorage replaces passwords through reset links.
type completeStorage interface {
	// ReadPasswordReset returns the [CompleteState] for the link with hash,
	// without a lock.
	ReadPasswordReset(ctx context.Context, hash [sha256.Size]byte) (CompleteState, error)

	// CompletePasswordReset locks the user of the link with hash, reads the
	// [CompleteState] for that link, and calls complete. If complete
	// succeeds, it saves the change's password hash, deletes all the user's
	// sessions, sign-in challenges, and reset links, deletes the sign-in
	// attempts counted for the user's email, and records a password-reset
	// event with the user as target and no actor, all in one transaction,
	// and returns the user. TOTP and backup codes stay. An error from
	// complete rolls back and is returned unchanged.
	CompletePasswordReset(ctx context.Context, hash [sha256.Size]byte, complete func(CompleteState) (CompleteChange, error)) (account.User, error)
}

// Completion replaces passwords through reset links.
type Completion struct {
	storage completeStorage
	now     func() time.Time
}

// NewCompletion returns a Completion given storage and a clock.
func NewCompletion(storage completeStorage, now func() time.Time) *Completion {
	return &Completion{storage: storage, now: now}
}

// Complete sets password as the password of the user the reset link with
// secret was sent to, signs that user out everywhere, and returns the user.
// It signs no one in. It returns [account.ErrPasswordInvalid] for a password
// outside the length rule, [ErrLinkInvalid] for a link that is malformed,
// unknown, used, expired, sent to an email the user no longer has, or owned
// by a deactivated user, and the context's error if it ends while waiting
// to hash.
//
// DESIGN: Complete has no throttle. The secret has 256 random bits, so
// guessing one is hopeless however fast the guesses come. Guesses are
// refused before the Argon2id hash, and passwordhash bounds the hashes in
// progress across the process. Every refusal is the same ErrLinkInvalid, so
// a holder of an old link cannot tell a deactivated account from a used
// link.
func (c *Completion) Complete(ctx context.Context, secret, password string) (account.User, error) {
	// 1. Refuse what needs no storage: a bad password or a malformed secret.
	if err := account.CheckPassword(password); err != nil {
		return account.User{}, err
	}
	hash, err := randomsecret.Hash(secret)
	if err != nil {
		return account.User{}, ErrLinkInvalid
	}

	// 2. Check the link without a lock, so a guessed secret costs no hash.
	state, err := c.storage.ReadPasswordReset(ctx, hash)
	if err != nil {
		return account.User{}, err
	}
	if err := checkCompletion(state, c.now().UTC()); err != nil {
		return account.User{}, err
	}

	// 3. Hash before storage takes its lock: Argon2id is slow.
	passwordHash, err := passwordhash.Hash(ctx, password)
	if err != nil {
		return account.User{}, err
	}
	return c.storage.CompletePasswordReset(ctx, hash, func(state CompleteState) (CompleteChange, error) {
		// 4. Check again under the lock: another completion or a
		// deactivation may have committed during the hash.
		if err := checkCompletion(state, c.now().UTC()); err != nil {
			return CompleteChange{}, err
		}
		return CompleteChange{PasswordHash: passwordHash}, nil
	})
}

// checkCompletion returns [ErrLinkInvalid] unless state's link is live at
// now, was sent to its user's current email, and the user is active.
func checkCompletion(state CompleteState, now time.Time) error {
	if state.Link == nil || !state.Link.ExpiresAt.After(now) || state.Deactivated {
		return ErrLinkInvalid
	}
	// The link saved an exact copy of the email, which is already in
	// emailaddress.Parse's form, so any difference is another address.
	if state.Link.Email != state.User.Email {
		return ErrLinkInvalid
	}
	return nil
}
