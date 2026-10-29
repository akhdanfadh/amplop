package session

import (
	"context"
	"crypto/sha256"
	"errors"
	"strconv"
	"time"

	"inbok/internal/totp"
)

// EnrollmentState is loaded while the user and current session are locked.
type EnrollmentState struct {
	Session State
	Factor  *totp.Factor
}

// EnrollmentChange must be committed together, without storing display codes.
type EnrollmentChange struct {
	Factor       totp.Factor
	Session      State
	BackupHashes []string
	AttemptID    int64
}

type enrollmentStore interface {
	// ConfirmTOTP locks the user, then the session, and loads the factor.
	// A missing user or session returns ErrNotFound. On callback success it saves
	// the factor and hashes, replaces all user sessions with the new session,
	// and removes this user's successful attempt in the same transaction.
	// Any error rolls back every write, including attempt removal.
	ConfirmTOTP(ctx context.Context, userID int64, hash [sha256.Size]byte, update func(EnrollmentState) (EnrollmentChange, error)) error
}

type secretOpener interface {
	Open(keyID string, ciphertext []byte, owner string) ([]byte, error)
}

type codeAttempts interface {
	Begin(ctx context.Context, userID int64) (int64, error)
}

// Enrollment confirms a factor and replaces sessions in one storage operation.
// It lives here because session-secret generation is private to this package.
type Enrollment struct {
	storage    enrollmentStore
	encryption secretOpener
	verifier   *totp.Verifier
	throttle   codeAttempts
	now        func() time.Time
}

// NewEnrollment supplies confirmation's storage, code checks, and clock.
func NewEnrollment(storage enrollmentStore, encryption secretOpener, verifier *totp.Verifier, throttle codeAttempts, now func() time.Time) *Enrollment {
	return &Enrollment{storage: storage, encryption: encryption, verifier: verifier, throttle: throttle, now: now}
}

// Confirmed reveals codes and a replacement cookie secret only after commit.
type Confirmed struct {
	Session     Issued
	BackupCodes []string
}

// Confirm requires a current TOTP code for the pending factor.
func (e *Enrollment) Confirm(ctx context.Context, current Authenticated, code string) (Confirmed, error) {
	// Go permits an empty Authenticated value; reject it before reserving an attempt.
	userID := current.Identity.User.ID
	if current.Session == (Reference{}) || userID <= 0 {
		return Confirmed{}, ErrUnauthenticated
	}

	attemptID, err := e.throttle.Begin(ctx, userID)
	if err != nil {
		return Confirmed{}, err
	}

	// Hash all ten codes before storage takes a write lock.
	backups, err := totp.NewBackupCodes()
	if err != nil {
		return Confirmed{}, err
	}
	issued := newSession(userID, e.now)

	err = e.storage.ConfirmTOTP(ctx, userID, current.Session.hash, func(state EnrollmentState) (EnrollmentChange, error) {
		// Current's earlier session check can become stale; recheck under the lock.
		if state.Session.UserID != userID || state.Session.TokenHash != current.Session.hash {
			return EnrollmentChange{}, ErrUnauthenticated
		}
		if _, err := checkSession(state.Session, e.now); err != nil {
			return EnrollmentChange{}, ErrUnauthenticated
		}

		if state.Factor == nil {
			return EnrollmentChange{}, totp.ErrNotPending
		}
		if state.Factor.ConfirmedAt != nil {
			return EnrollmentChange{}, totp.ErrAlreadyOn
		}

		// Bind decryption to this owner before checking the code and replay history.
		secret, err := e.encryption.Open(state.Factor.KeyID, state.Factor.Ciphertext, "totp:"+strconv.FormatInt(userID, 10))
		if err != nil {
			return EnrollmentChange{}, err
		}

		step, err := e.verifier.Check(string(secret), code, state.Factor.LastStep)
		if err != nil {
			return EnrollmentChange{}, err
		}

		// Describe the change without mutating the loaded factor before commit.
		factor := *state.Factor
		now := e.now().UTC()
		factor.ConfirmedAt, factor.LastStep = &now, step

		return EnrollmentChange{
			Factor:       factor,
			Session:      issued.State,
			BackupHashes: backups.Hashes,
			AttemptID:    attemptID,
		}, nil
	})

	if errors.Is(err, ErrNotFound) {
		return Confirmed{}, ErrUnauthenticated
	}
	if err != nil {
		return Confirmed{}, err
	}

	// Reveal the cookie secret and display codes only after storage commits.
	return Confirmed{Session: issued, BackupCodes: backups.Codes}, nil
}
