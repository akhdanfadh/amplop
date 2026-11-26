package passwordreset

import (
	"context"
	"crypto/sha256"
	"time"

	"amplop/internal/account"
	"amplop/internal/emailaddress"
	"amplop/internal/passwordhash"
	"amplop/internal/randomsecret"
)

// CompleteState is what completeStorage reads for one reset link.
type CompleteState struct {
	Link        *Link        // nil: no link has the secret's hash
	User        account.User // the link's user; zero without a link
	Deactivated bool         // the link's user is deactivated
}

// CompleteChange is the new password's hash and when it changed.
type CompleteChange struct {
	PasswordHash string
	// The notice to the owner names it, so a reset they did not ask for
	// does not go unnoticed (OWASP's Forgot Password Cheat Sheet).
	ChangedAt time.Time
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
	// event with the user as target and no actor, and saves the job that
	// tells the user their password changed at the change's time, all in
	// one transaction, and returns the user. TOTP and backup codes stay. An
	// error from complete rolls back and is returned unchanged.
	CompletePasswordReset(ctx context.Context, hash [sha256.Size]byte, complete func(CompleteState) (CompleteChange, error)) (account.User, error)
}

// Completion replaces passwords through reset links.
type Completion struct {
	storage completeStorage
	now     func() time.Time
}

// NewCompletion returns a [Completion] given storage and a clock.
func NewCompletion(storage completeStorage, now func() time.Time) *Completion {
	return &Completion{storage: storage, now: now}
}

// Complete sets password as the password of the user the reset link with
// secret was sent to, when email is that link's email, signs that user out
// everywhere, and returns the user. It signs no one in. With the reset it
// saves a background job that emails the user that their password changed.
// It returns [account.ErrPasswordInvalid] for a password outside the length
// rule, [emailaddress.ErrInvalid] for an invalid email,
// [ErrLinkInvalid] for a link that is malformed, unknown, used, expired,
// sent to an email the user no longer has, or owned by a deactivated user,
// [ErrEmailMismatch] for a live link sent to another email, and the
// context's error if it ends while waiting to hash.
//
// DESIGN: Complete has no throttle. The secret has 256 random bits, so
// guessing one is hopeless however fast the guesses come. Guesses are
// refused before the Argon2id hash, and passwordhash bounds the hashes in
// progress across the process. Every refusal of the link itself is the same
// [ErrLinkInvalid], so a holder of an old link cannot tell a deactivated
// account from a used link.
//
// DESIGN: email must be the link's, so a link someone sends for an account
// they control cannot set its password for a person who types their own
// address (link fixation).
func (c *Completion) Complete(ctx context.Context, secret, email, password string) (account.User, error) {
	// 1. Refuse what needs no storage: a bad password, an invalid email, or
	// a malformed secret.
	if err := account.ValidatePassword(password); err != nil {
		return account.User{}, err
	}
	email, err := emailaddress.Parse(email)
	if err != nil {
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
	if err := checkCompletion(state, email, c.now().UTC()); err != nil {
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
		changedAt := c.now().UTC()
		if err := checkCompletion(state, email, changedAt); err != nil {
			return CompleteChange{}, err
		}
		return CompleteChange{PasswordHash: passwordHash, ChangedAt: changedAt}, nil
	})
}

// checkCompletion returns [ErrLinkInvalid] unless state's link is live at
// now, was sent to its user's current email, and the user is active, and
// [ErrEmailMismatch] unless email is that link's.
func checkCompletion(state CompleteState, email string, now time.Time) error {
	if state.Link == nil || !state.Link.ExpiresAt.After(now) || state.Deactivated {
		return ErrLinkInvalid
	}
	// The link saved an exact copy of the email, which is already in
	// emailaddress.Parse's form, so any difference is another address.
	if state.Link.Email != state.User.Email {
		return ErrLinkInvalid
	}
	// Both are in emailaddress.Parse's form, so any difference is another
	// address.
	if email != state.Link.Email {
		return ErrEmailMismatch
	}
	return nil
}
