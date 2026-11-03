package registration

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"inbok/internal/account"
	"inbok/internal/emailaddress"
	"inbok/internal/passwordhash"
	"inbok/internal/randomsecret"
)

var (
	// ErrLinkInvalid means a registration link is malformed, unknown, used,
	// or expired.
	ErrLinkInvalid = errors.New("registration link is not valid")
	// ErrAccountExists means the link's email has an account, e.g. one an
	// earlier submission of the same link just created.
	ErrAccountExists = errors.New("email already has an account")
	// ErrEmailMismatch means a live registration link was sent to another
	// email than the one given with it.
	ErrEmailMismatch = errors.New("registration link was sent to another email")
)

// CompleteState is what [completeStorage] reads for one registration link.
type CompleteState struct {
	Registration  Record
	Link          *Link // nil: no link has the secret's hash
	AccountExists bool  // an account has the email asked about
}

// CompleteChange is the password and verified time of the member to create.
type CompleteChange struct {
	PasswordHash string
	VerifiedAt   time.Time
}

// completeStorage creates members from registration links.
type completeStorage interface {
	// ReadCompletion returns the [CompleteState] for the link with hash and
	// its email, without a lock.
	ReadCompletion(ctx context.Context, hash [sha256.Size]byte) (CompleteState, error)

	// CompleteRegistration locks the registration state, the system email
	// settings, user writes, and the link with hash, reads the
	// [CompleteState] for that link and email, and calls create. If create
	// succeeds, it creates a member with the link's email and the change,
	// deletes every registration link to that email, and records a
	// member-registered event, all in one transaction, and returns the
	// member. An error from create rolls back and is returned unchanged.
	CompleteRegistration(ctx context.Context, hash [sha256.Size]byte, email string, create func(CompleteState) (CompleteChange, error)) (account.User, error)
}

// Completion creates member accounts from registration links.
type Completion struct {
	storage completeStorage
	now     func() time.Time
}

// NewCompletion returns a Completion given storage and a clock.
func NewCompletion(storage completeStorage, now func() time.Time) *Completion {
	return &Completion{storage: storage, now: now}
}

// Complete creates a member with password and a verified email, the one the
// registration link with secret was sent to, when email is that one, and
// returns the member. It creates no session. It returns
// [account.ErrPasswordInvalid] for a password outside the length rule,
// [emailaddress.ErrInvalid] for an invalid email, [ErrClosed] unless
// registration is open, [ErrAccountExists] when the link's email already has
// an account, [ErrLinkInvalid] for a link that is malformed, unknown, used,
// or expired, [ErrEmailMismatch] for a live link sent to another email, and
// the context's error if it ends while waiting to hash.
//
// NOTE: Complete has no throttle. The secret has 256 random bits, so guessing
// one is hopeless however fast the guesses come. Guesses are refused before
// the Argon2id hash, and passwordhash bounds the hashes in progress across
// the process, so one live link sent many times at once cannot exhaust
// memory.
//
// DESIGN: email must be the link's, so a link someone sends for an address
// they control cannot create that account for a person who types their own
// (link fixation, ADR028).
func (c *Completion) Complete(ctx context.Context, secret, email, password string) (account.User, error) {
	// 1. Refuse what needs no storage: a bad password, an invalid email, or
	// a malformed secret.
	if err := account.CheckPassword(password); err != nil {
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
	state, err := c.storage.ReadCompletion(ctx, hash)
	if err != nil {
		return account.User{}, err
	}
	if err := checkCompletion(state, email, c.now().UTC()); err != nil {
		return account.User{}, err
	}
	// The link decides the account's email; the one typed only had to match.
	linkEmail := state.Link.Email

	// 3. Hash before storage takes its locks: Argon2id is slow.
	passwordHash, err := passwordhash.Hash(ctx, password)
	if err != nil {
		return account.User{}, err
	}
	return c.storage.CompleteRegistration(ctx, hash, linkEmail, func(state CompleteState) (CompleteChange, error) {
		// 4. Check again under the locks: anything may have changed during
		// the hash.
		now := c.now().UTC()
		if err := checkCompletion(state, email, now); err != nil {
			return CompleteChange{}, err
		}
		return CompleteChange{PasswordHash: passwordHash, VerifiedAt: now}, nil
	})
}

// checkCompletion returns why state cannot create a member at now for email,
// or nil.
func checkCompletion(state CompleteState, email string, now time.Time) error {
	// Closing deletes every link, so this comes first: a visitor whose link
	// stopped working learns that registration closed.
	if statusOf(state.Registration) != StatusOpen {
		return ErrClosed
	}
	// A completion deletes every link to its email, so a submission that
	// waited for another of the same link finds none: say why.
	if state.AccountExists {
		return ErrAccountExists
	}
	if state.Link == nil || !state.Link.ExpiresAt.After(now) {
		return ErrLinkInvalid
	}
	// The link saved an exact copy of the email, which is already in
	// emailaddress.Parse's form, so any difference is another address.
	if email != state.Link.Email {
		return ErrEmailMismatch
	}
	return nil
}
