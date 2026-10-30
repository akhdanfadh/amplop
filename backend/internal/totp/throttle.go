package totp

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

const (
	// Product policy: OWASP recommends strict limits without prescribing these values.
	shortWindow = 15 * time.Minute
	shortLimit  = 5
	longWindow  = 24 * time.Hour
	longLimit   = 20
)

// ErrThrottled means recent code attempts have exhausted a user's allowance.
var ErrThrottled = errors.New("TOTP code attempts throttled")

var errAttemptUserID = errors.New("invalid TOTP attempt user ID")

// Attempt is reserved before checking a code. It contains no code or secret.
type Attempt struct {
	UserID    int64
	CreatedAt time.Time
}

// AttemptStorage stores code attempts and serializes reservations per user.
type AttemptStorage interface {
	// ReserveTOTPAttempt removes expired rows, inserts the attempt, then calls
	// check with this user's rows, including it. A check error rolls back writes.
	ReserveTOTPAttempt(ctx context.Context, attempt Attempt, cutoff time.Time, check func([]Attempt) error) (int64, error)

	// DeleteTOTPAttempt removes a successful attempt. Missing rows are harmless.
	DeleteTOTPAttempt(ctx context.Context, id int64) error
}

// Throttle counts failed and in-flight code checks across all TOTP operations.
type Throttle struct {
	storage AttemptStorage
	now     func() time.Time
	logger  *slog.Logger
}

// NewThrottle supplies storage, a clock, and a logger for user-ID-only warnings.
func NewThrottle(storage AttemptStorage, now func() time.Time, logger *slog.Logger) *Throttle {
	return &Throttle{storage: storage, now: now, logger: logger}
}

// Begin reserves an attempt before verification. Leave it saved on failure.
// Successful verification must remove only its own reservation with Succeed.
func (t *Throttle) Begin(ctx context.Context, userID int64) (int64, error) {
	if userID <= 0 {
		return 0, errAttemptUserID
	}
	now := t.now().UTC()
	cutoff := now.Add(-longWindow)
	shortCutoff := now.Add(-shortWindow)
	id, err := t.storage.ReserveTOTPAttempt(ctx, Attempt{UserID: userID, CreatedAt: now}, cutoff, func(rows []Attempt) error {
		var shortCount, longCount int
		for _, row := range rows {
			if row.UserID != userID || !row.CreatedAt.After(cutoff) {
				continue
			}
			longCount++
			if row.CreatedAt.After(shortCutoff) {
				shortCount++
			}
		}
		if shortCount > shortLimit || longCount > longLimit {
			return ErrThrottled
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
