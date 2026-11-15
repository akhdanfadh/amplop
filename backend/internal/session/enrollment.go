package session

import (
	"context"
	"crypto/sha256"
	"errors"
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
// active backup codes; storage deletes the waiting set, display copy
// included.
type EnrollmentChange struct {
	Factor       totp.Factor
	Session      State
	BackupHashes []string
}

type enrollmentStore interface {
	// FinishTOTP locks the user, then the session, and loads the factor and
	// waiting set. A missing user or session, or a deactivated user, returns
	// [ErrNotFound]. On callback success it saves the factor, makes the
	// hashes the active backup codes, deletes the waiting set, and replaces
	// all user sessions with the new session, in the same transaction.
	// Any error rolls back every write.
	FinishTOTP(ctx context.Context, userID int64, hash [sha256.Size]byte, update func(EnrollmentState) (EnrollmentChange, error)) error
}

// Enrollment turns two-factor authentication on and replaces sessions in one
// storage operation. It lives here because session-secret generation is
// private to this package.
type Enrollment struct {
	storage enrollmentStore
	now     func() time.Time
}

// NewEnrollment supplies finishing's storage and clock.
func NewEnrollment(storage enrollmentStore, now func() time.Time) *Enrollment {
	return &Enrollment{storage: storage, now: now}
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
