package totp

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"amplop/internal/attemptlimit"
)

// Product policy: OWASP recommends strict limits without prescribing these values.
var (
	shortLimit = attemptlimit.Limit{Window: 15 * time.Minute, Max: 5}
	longLimit  = attemptlimit.Limit{Window: 24 * time.Hour, Max: 20}
)

// ErrThrottled means recent code attempts have exhausted a user's allowance.
var ErrThrottled = errors.New("TOTP code attempts throttled")

var errAttemptUserID = errors.New("invalid TOTP attempt user ID")

// releaseTimeout bounds [Throttle.Release], which outlives a cancelled request.
const releaseTimeout = 5 * time.Second

// Attempt is reserved before checking a code. It contains no code or secret.
type Attempt struct {
	UserID    int64
	CreatedAt time.Time
}

// AttemptStorage stores code attempts and serializes reservations per user.
type AttemptStorage interface {
	// ReserveTOTPAttempt removes this user's rows created at or before cutoff,
	// calls check with the remaining ones, then saves the attempt and returns
	// its ID. A check error rolls back writes and saves nothing.
	ReserveTOTPAttempt(ctx context.Context, attempt Attempt, cutoff time.Time, check func([]Attempt) error) (int64, error)

	// DeleteTOTPAttempt removes an attempt by ID, after success or on release.
	// Missing rows are harmless.
	DeleteTOTPAttempt(ctx context.Context, id int64) error
}

// Throttle counts failed and in-flight code checks across all TOTP operations.
type Throttle struct {
	storage AttemptStorage
	now     func() time.Time
	logger  *slog.Logger
}

// NewThrottle supplies storage, a clock, and a logger for warnings that name
// no code or secret.
func NewThrottle(storage AttemptStorage, now func() time.Time, logger *slog.Logger) *Throttle {
	return &Throttle{storage: storage, now: now, logger: logger}
}

// Begin reserves an attempt for userID before verification and returns its ID,
// or an [*attemptlimit.Error] wrapping [ErrThrottled] when one more does not
// fit. Leave it saved when a code fails. Success removes only its own
// reservation, in the operation's storage transaction or with
// [Throttle.Succeed]; a request that fails before checking a code removes it
// with [Throttle.Release].
func (t *Throttle) Begin(ctx context.Context, userID int64) (int64, error) {
	// 1. Refuse an invalid user ID before storage.
	if userID <= 0 {
		return 0, errAttemptUserID
	}

	// 2. Reserve the attempt if the user's earlier ones leave room.
	now := t.now().UTC()
	cutoff := now.Add(-longLimit.Window)
	id, err := t.storage.ReserveTOTPAttempt(ctx, Attempt{UserID: userID, CreatedAt: now}, cutoff, func(rows []Attempt) error {
		var times []time.Time
		for _, row := range rows {
			if row.UserID == userID {
				times = append(times, row.CreatedAt)
			}
		}
		if wait := attemptlimit.Wait(times, now, shortLimit, longLimit); wait > 0 {
			return &attemptlimit.Error{Err: ErrThrottled, Wait: wait}
		}
		return nil
	})
	if errors.Is(err, ErrThrottled) {
		t.logger.WarnContext(ctx, "TOTP code attempts throttled", "user_id", userID)
	}
	return id, err
}

// Succeed removes this attempt after the protected action has succeeded.
func (t *Throttle) Succeed(ctx context.Context, id int64) error {
	return t.storage.DeleteTOTPAttempt(ctx, id)
}

// Release removes attempt id when its request ended before checking any
// code, so only guesses count. It logs a failure instead of returning it.
//
// DESIGN: the request already failed with its own error, which is the one to
// report. An attempt that cannot be removed counts until it expires, as a
// wrong code would, so the failure costs the user one attempt, not safety.
func (t *Throttle) Release(ctx context.Context, id int64) {
	// Remove it even if the client has gone, keeping the request's values,
	// but do not hold the request open on a stuck database.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	if err := t.storage.DeleteTOTPAttempt(ctx, id); err != nil {
		t.logger.WarnContext(ctx, "TOTP attempt not released", "attempt_id", id, "error", err)
	}
}
