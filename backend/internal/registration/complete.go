package registration

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"inbok/internal/account"
	"inbok/internal/passwordhash"
	"inbok/internal/randomsecret"
)

// maxHashes bounds Argon2id hashes in progress. Each takes 19 MiB, and one
// live link lets anyone start a hash per parallel request.
const maxHashes = 4

var (
	// ErrLinkInvalid means a registration link is malformed, unknown, used,
	// or expired.
	ErrLinkInvalid = errors.New("registration link is not valid")
	// ErrAccountExists means the link's email has an account, e.g. one an
	// earlier submission of the same link just created.
	ErrAccountExists = errors.New("email already has an account")
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
	hashes  chan struct{} // one slot per hash in progress
}

// NewCompletion returns a Completion given storage and a clock.
func NewCompletion(storage completeStorage, now func() time.Time) *Completion {
	return &Completion{storage: storage, now: now, hashes: make(chan struct{}, maxHashes)}
}

// Complete creates a member with password and a verified email, the one the
// registration link with secret was sent to, and returns the member. It
// creates no session. It returns [account.ErrPasswordInvalid] for a password
// outside the length rule, [ErrClosed] unless registration is open,
// [ErrAccountExists] when the email already has an account, [ErrLinkInvalid]
// for a link that is malformed, unknown, used, or expired, and the context's
// error if it ends while waiting to hash.
//
// NOTE: Complete has no throttle. The secret has 256 random bits, so guessing
// one is hopeless however fast the guesses come. Guesses are refused before
// the Argon2id hash, and maxHashes bounds the hashes one live link can start.
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
	state, err := c.storage.ReadCompletion(ctx, hash)
	if err != nil {
		return account.User{}, err
	}
	if err := checkCompletion(state, c.now().UTC()); err != nil {
		return account.User{}, err
	}
	email := state.Link.Email

	// 3. Hash before storage takes its locks: Argon2id is slow.
	passwordHash, err := c.hash(ctx, password)
	if err != nil {
		return account.User{}, err
	}
	return c.storage.CompleteRegistration(ctx, hash, email, func(state CompleteState) (CompleteChange, error) {
		// 4. Check again under the locks: anything may have changed during
		// the hash.
		now := c.now().UTC()
		if err := checkCompletion(state, now); err != nil {
			return CompleteChange{}, err
		}
		return CompleteChange{PasswordHash: passwordHash, VerifiedAt: now}, nil
	})
}

// hash returns password's Argon2id hash once a slot is free, or the
// context's error if it ends first.
func (c *Completion) hash(ctx context.Context, password string) (string, error) {
	select {
	case c.hashes <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-c.hashes }()

	return passwordhash.Hash(password), nil
}

// checkCompletion returns why state cannot create a member at now, or nil.
func checkCompletion(state CompleteState, now time.Time) error {
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
	return nil
}
