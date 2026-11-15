package totp

import (
	"context"
	"errors"
	"time"
)

// ErrNotOn means a confirmed authenticator is required for this operation.
var ErrNotOn = errors.New("TOTP is not on")

type opener interface {
	Open(keyID string, ciphertext []byte, owner string) ([]byte, error)
}

type codeAttempts interface {
	Begin(ctx context.Context, userID int64) (int64, error)
	Release(ctx context.Context, id int64)
}

// Backups replaces recovery codes in two steps: a fresh authenticator code
// makes a new set that waits while the old one still works, and its one-time
// token swaps them. The new codes appear only in the first step's answer.
type Backups struct {
	storage    Storage
	encryption opener
	verifier   *Verifier
	throttle   codeAttempts
	now        func() time.Time
}

// NewBackups supplies storage, owner-bound decryption of the authenticator
// secret, the code verifier and attempt limit, and a clock.
func NewBackups(storage Storage, encryption opener, verifier *Verifier, throttle codeAttempts, now func() time.Time) *Backups {
	return &Backups{storage: storage, encryption: encryption, verifier: verifier, throttle: throttle, now: now}
}

// Replace requires a checked user's ID and a fresh TOTP code, not a backup
// code. It makes a new set that waits, replacing any earlier waiting set;
// the active codes keep working until [Backups.Finish]. It returns
// [ErrNotOn], [ErrInvalidCode], or the attempt limit's error.
func (s *Backups) Replace(ctx context.Context, userID int64, code string) (Codes, error) {
	// 1. Reserve an attempt before any code is verified.
	attemptID, err := s.throttle.Begin(ctx, userID)
	if err != nil {
		return Codes{}, err
	}

	// 2. Make the codes before taking the owner's write lock: salted hashes
	// are slow. They keep no display copy, so nothing can show them again.
	set, err := newWaitingSet(ctx)
	if err != nil {
		// No code was verified.
		s.throttle.Release(ctx, attemptID)
		return Codes{}, err
	}

	verified := false
	err = s.storage.UpdateTOTP(ctx, userID, func(current Record) (Change, error) {
		// 3. Under the owner's lock, refuse when TOTP is off.
		factor := current.Factor
		if factor == nil || factor.ConfirmedAt == nil {
			return Change{}, ErrNotOn
		}

		// 4. Decrypt the secret and verify the code.
		secret, err := s.encryption.Open(factor.KeyID, factor.Ciphertext, secretOwner(userID))
		if err != nil {
			return Change{}, err
		}
		verified = true
		step, err := s.verifier.Verify(string(secret), code, factor.LastStep)
		if err != nil {
			return Change{}, err
		}

		// Describe the write without mutating the loaded factor before commit.
		next := *factor
		next.LastStep = step
		return Change{
			Factor: &next, WaitingWrite: SaveWaiting, Waiting: set.waiting(s.now().Add(enrollmentLifetime)),
			AttemptID: attemptID,
		}, nil
	})

	// 5. Release the attempt when no code was verified. A request refused
	// before that, e.g. with TOTP off, made no guess.
	if err != nil {
		if !verified {
			s.throttle.Release(ctx, attemptID)
		}
		return Codes{}, err
	}

	// 6. Return the codes and token only after they are saved. A lost answer
	// loses them; a fresh code makes another set.
	return set.codes(), nil
}

// Finish activates the waiting set token belongs to, replacing every active
// code, and returns how many codes are now active. It returns [ErrNotOn]
// when TOTP is off, and [ErrNotWaiting] when no live set waits for token.
func (s *Backups) Finish(ctx context.Context, userID int64, token string) (int, error) {
	var active int
	err := s.storage.UpdateTOTP(ctx, userID, func(current Record) (Change, error) {
		if current.Factor == nil || current.Factor.ConfirmedAt == nil {
			return Change{}, ErrNotOn
		}
		if err := VerifyWaiting(current.Waiting, token, s.now()); err != nil {
			return Change{}, err
		}
		active = len(current.Waiting.Hashes)
		return Change{WaitingWrite: DeleteWaiting, BackupHashes: current.Waiting.Hashes}, nil
	})
	if err != nil {
		return 0, err
	}
	return active, nil
}
