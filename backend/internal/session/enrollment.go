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
	Waiting *totp.Waiting
}

// EnrollmentChange must be committed together. BackupHashes become the
// active backup codes; finishing also deletes the waiting set, display copy
// included. AttemptID, when not zero, removes the owner's successful
// attempt.
type EnrollmentChange struct {
	Factor       totp.Factor
	Session      State
	BackupHashes []string
	AttemptID    int64
}

type enrollmentStore interface {
	// ConfirmTOTP locks the user, then the session, and loads the factor.
	// A missing user or session, or a deactivated user, returns [ErrNotFound].
	// On callback success it saves the factor and hashes, replaces all user
	// sessions with the new session, and removes this user's successful
	// attempt in the same transaction.
	// Any error rolls back every write, including attempt removal.
	ConfirmTOTP(ctx context.Context, userID int64, hash [sha256.Size]byte, update func(EnrollmentState) (EnrollmentChange, error)) error

	// FinishTOTP locks the user, then the session, and loads the factor and
	// waiting set. A missing user or session, or a deactivated user, returns
	// [ErrNotFound]. On callback success it saves the factor, makes the
	// hashes the active backup codes, deletes the waiting set, and replaces
	// all user sessions with the new session, in the same transaction.
	// Any error rolls back every write.
	FinishTOTP(ctx context.Context, userID int64, hash [sha256.Size]byte, update func(EnrollmentState) (EnrollmentChange, error)) error
}

type secretOpener interface {
	Open(keyID string, ciphertext []byte, owner string) ([]byte, error)
}

type codeAttempts interface {
	Begin(ctx context.Context, userID int64) (int64, error)
	Release(ctx context.Context, id int64)
}

// Enrollment turns two-factor authentication on and replaces sessions in one
// storage operation. It lives here because session-secret generation is
// private to this package.
type Enrollment struct {
	storage    enrollmentStore
	encryption secretOpener
	verifier   *totp.Verifier
	throttle   codeAttempts
	now        func() time.Time
}

// NewEnrollment supplies storage, confirmation's code checks, and a clock.
func NewEnrollment(storage enrollmentStore, encryption secretOpener, verifier *totp.Verifier, throttle codeAttempts, now func() time.Time) *Enrollment {
	return &Enrollment{storage: storage, encryption: encryption, verifier: verifier, throttle: throttle, now: now}
}

// Confirmed reveals codes and a replacement cookie secret only after commit.
type Confirmed struct {
	Session     Issued
	BackupCodes []string
}

// Confirm requires a current TOTP code for the pending factor.
// The replacement session keeps the device details of this request.
func (e *Enrollment) Confirm(ctx context.Context, current Authenticated, code string, device Device) (Confirmed, error) {
	// 1. Refuse an empty Authenticated value, which Go permits, before
	// reserving an attempt.
	userID := current.Identity.User.ID
	if current.Session == (Reference{}) || userID <= 0 {
		return Confirmed{}, ErrUnauthenticated
	}

	// 2. Reserve an attempt before any code is verified.
	attemptID, err := e.throttle.Begin(ctx, userID)
	if err != nil {
		return Confirmed{}, err
	}

	// 3. Hash all ten backup codes before storage takes a write lock.
	backups, err := totp.NewBackupCodes(ctx)
	if err != nil {
		// No code was verified.
		e.throttle.Release(ctx, attemptID)
		return Confirmed{}, err
	}
	issued := newSession(userID, device, e.now)

	verified := false
	err = e.storage.ConfirmTOTP(ctx, userID, current.Session.hash, func(state EnrollmentState) (EnrollmentChange, error) {
		// 4. Check the session again under the lock: Current's earlier check
		// can become stale.
		if state.Session.UserID != userID || state.Session.TokenHash != current.Session.hash {
			return EnrollmentChange{}, ErrUnauthenticated
		}
		if _, err := checkSession(state.Session, e.now); err != nil {
			return EnrollmentChange{}, ErrUnauthenticated
		}

		// 5. Refuse when no factor is pending.
		if state.Factor == nil {
			return EnrollmentChange{}, totp.ErrNotPending
		}
		if state.Factor.ConfirmedAt != nil {
			return EnrollmentChange{}, totp.ErrAlreadyOn
		}

		// 6. Decrypt the secret, bound to this owner, before verifying the
		// code and its replay history.
		secret, err := e.encryption.Open(state.Factor.KeyID, state.Factor.Ciphertext, "totp:"+strconv.FormatInt(userID, 10))
		if err != nil {
			return EnrollmentChange{}, err
		}

		// 7. Verify the code.
		verified = true
		step, err := e.verifier.Verify(string(secret), code, state.Factor.LastStep)
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

	// 8. Release the attempt when no code was verified. A request refused
	// before that, e.g. with nothing pending or a stale session, made no
	// guess.
	if err != nil && !verified {
		e.throttle.Release(ctx, attemptID)
	}
	if errors.Is(err, ErrNotFound) {
		return Confirmed{}, ErrUnauthenticated
	}
	if err != nil {
		return Confirmed{}, err
	}

	// 9. Reveal the cookie secret and display codes only after storage
	// commits.
	return Confirmed{Session: issued, BackupCodes: backups.Codes}, nil
}

// Finished is a finished enrollment: the replacement session, and how many
// backup codes are now active.
type Finished struct {
	Issued
	BackupCodesLeft int
}

// Finish turns two-factor authentication on once the person has saved the
// backup codes that token came with: the enrollment's code was checked, and
// the codes become active. It ends every session and returns the
// replacement, which keeps the device details of this request, with the
// count of active codes. It returns [totp.ErrNotPending],
// [totp.ErrAlreadyOn], [totp.ErrEnrollmentExpired], or [totp.ErrNotWaiting]
// when no live set waits for token.
func (e *Enrollment) Finish(ctx context.Context, current Authenticated, token string, device Device) (Finished, error) {
	// 1. Refuse an empty Authenticated value, which Go permits.
	userID := current.Identity.User.ID
	if current.Session == (Reference{}) || userID <= 0 {
		return Finished{}, ErrUnauthenticated
	}

	issued := newSession(userID, device, e.now)
	var active int
	err := e.storage.FinishTOTP(ctx, userID, current.Session.hash, func(state EnrollmentState) (EnrollmentChange, error) {
		// 2. Check the session again under the lock: Current's earlier check
		// can become stale.
		if state.Session.UserID != userID || state.Session.TokenHash != current.Session.hash {
			return EnrollmentChange{}, ErrUnauthenticated
		}
		if _, err := checkSession(state.Session, e.now); err != nil {
			return EnrollmentChange{}, ErrUnauthenticated
		}

		// 3. Refuse anything but a live enrollment whose codes wait for this
		// token. The token proves this browser received the codes, which a
		// stolen session cookie alone cannot.
		now := e.now()
		if state.Factor == nil {
			return EnrollmentChange{}, totp.ErrNotPending
		}
		if state.Factor.ConfirmedAt != nil {
			return EnrollmentChange{}, totp.ErrAlreadyOn
		}
		if err := totp.CheckEnrollment(*state.Factor, now); err != nil {
			return EnrollmentChange{}, err
		}
		if err := totp.VerifyWaiting(state.Waiting, token, now); err != nil {
			return EnrollmentChange{}, err
		}

		// Describe the change without mutating the loaded factor before commit.
		factor := *state.Factor
		confirmedAt := now.UTC()
		factor.ConfirmedAt = &confirmedAt
		active = len(state.Waiting.Hashes)
		return EnrollmentChange{Factor: factor, Session: issued.State, BackupHashes: state.Waiting.Hashes}, nil
	})
	if errors.Is(err, ErrNotFound) {
		return Finished{}, ErrUnauthenticated
	}
	if err != nil {
		return Finished{}, err
	}

	// 4. Reveal the cookie secret only after storage commits.
	return Finished{Issued: issued, BackupCodesLeft: active}, nil
}
