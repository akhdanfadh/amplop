package totp

import (
	"context"
	"errors"
	"strconv"
)

// ErrNotOn means a confirmed authenticator is required for this operation.
var ErrNotOn = errors.New("TOTP is not on")

// BackupChange replaces hashes and consumes a TOTP step and successful attempt.
type BackupChange struct {
	Factor    Factor
	Hashes    []string
	AttemptID int64
}

type backupStore interface {
	// ReplaceTOTPBackupCodes locks the owner, loads the factor, and calls update.
	// It saves the factor, replaces every backup hash, and removes the owned
	// successful attempt in one transaction. Any error rolls back every write.
	ReplaceTOTPBackupCodes(ctx context.Context, userID int64, update func(*Factor) (BackupChange, error)) error
}

type opener interface {
	Open(keyID string, ciphertext []byte, owner string) ([]byte, error)
}

type codeAttempts interface {
	Begin(ctx context.Context, userID int64) (int64, error)
	Release(ctx context.Context, id int64)
}

// Backups manages recovery codes using current authenticator proof.
type Backups struct {
	storage    backupStore
	encryption opener
	verifier   *Verifier
	throttle   codeAttempts
}

func NewBackups(storage backupStore, encryption opener, verifier *Verifier, throttle codeAttempts) *Backups {
	return &Backups{storage: storage, encryption: encryption, verifier: verifier, throttle: throttle}
}

// Regenerate requires a checked user's ID and a fresh TOTP code, not a backup code.
func (s *Backups) Regenerate(ctx context.Context, userID int64, code string) ([]string, error) {
	attemptID, err := s.throttle.Begin(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Prepare slow salted hashes before taking the owner's write lock.
	set, err := NewBackupCodes()
	if err != nil {
		// No code was checked.
		s.throttle.Release(ctx, attemptID)
		return nil, err
	}

	checked := false
	err = s.storage.ReplaceTOTPBackupCodes(ctx, userID, func(current *Factor) (BackupChange, error) {
		if current == nil || current.ConfirmedAt == nil {
			return BackupChange{}, ErrNotOn
		}

		secret, err := s.encryption.Open(current.KeyID, current.Ciphertext, "totp:"+strconv.FormatInt(userID, 10))
		if err != nil {
			return BackupChange{}, err
		}
		checked = true
		step, err := s.verifier.Check(string(secret), code, current.LastStep)
		if err != nil {
			return BackupChange{}, err
		}

		// Describe the write without mutating the loaded factor before commit.
		factor := *current
		factor.LastStep = step
		return BackupChange{Factor: factor, Hashes: set.Hashes, AttemptID: attemptID}, nil
	})
	if err != nil {
		// A request refused before any code check, e.g. with TOTP off, made
		// no guess, so it releases its attempt.
		if !checked {
			s.throttle.Release(ctx, attemptID)
		}
		return nil, err
	}

	// Cleanup committed with replacement; no later error can hide the only copy.
	return set.Codes, nil
}
