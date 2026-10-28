package session

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"time"
)

const (
	attemptWindow = 15 * time.Minute
	pairLimit     = 5
	addressLimit  = 50
)

// ErrThrottled means an attempt exceeds the sign-in failure limits.
var ErrThrottled = errors.New("sign-in throttled")

// Attempt is a sign-in or setup attempt counted toward the failure limits.
// It is reserved before verification. Email is nil for first-admin setup.
type Attempt struct {
	Email      *string
	ClientAddr string
	CreatedAt  time.Time
}

// AttemptStorage stores sign-in and setup attempts.
// Reservations for one client address must be serialized across API instances.
type AttemptStorage interface {
	// ReserveAttempt saves an attempt and returns its ID if check accepts it.
	// It removes old rows, inserts the attempt, then checks the address's rows
	// including it. A check error rolls back the reservation.
	ReserveAttempt(ctx context.Context, attempt Attempt, cutoff time.Time, check func([]Attempt) error) (int64, error)

	// DeleteAttempt removes an attempt by ID. A missing ID is harmless.
	DeleteAttempt(ctx context.Context, id int64) error
}

// Throttle is the failure limiter for sign-in and first-admin setup.
// Pending checks count as failures until Succeed removes them.
type Throttle struct {
	storage AttemptStorage
	now     func() time.Time
}

// NewThrottle creates a failure limiter using storage and the given clock.
func NewThrottle(storage AttemptStorage, now func() time.Time) *Throttle {
	return &Throttle{storage: storage, now: now}
}

// Begin reserves a sign-in attempt and returns its ID if the limits allow it.
// Call it before checking the password. Leave the row on failure.
func (t *Throttle) Begin(ctx context.Context, email string, addr netip.Addr) (int64, error) {
	// This lookup key matches lower(email) uniqueness; delivery case is unchanged.
	key := strings.ToLower(strings.TrimSpace(email))
	return t.begin(ctx, &key, addr)
}

// BeginSetup reserves a setup attempt and returns its ID.
// Only the address limit applies; setup attempts have no email key.
func (t *Throttle) BeginSetup(ctx context.Context, addr netip.Addr) (int64, error) {
	return t.begin(ctx, nil, addr)
}

// Succeed removes the reservation for a successful sign-in or setup attempt.
func (t *Throttle) Succeed(ctx context.Context, id int64) error {
	return t.storage.DeleteAttempt(ctx, id)
}

// begin reserves an attempt and checks the limits that apply to it.
func (t *Throttle) begin(ctx context.Context, email *string, addr netip.Addr) (int64, error) {
	if !addr.IsValid() {
		return 0, errors.New("invalid client address")
	}
	now := t.now().UTC()
	cutoff := now.Add(-attemptWindow)
	attempt := Attempt{Email: email, ClientAddr: addr.Unmap().WithZone("").String(), CreatedAt: now}
	return t.storage.ReserveAttempt(ctx, attempt, cutoff, func(rows []Attempt) error {
		var addressCount, pairCount int
		for _, row := range rows {
			if !row.CreatedAt.After(cutoff) {
				continue
			}
			addressCount++
			if email != nil && row.Email != nil && *email == *row.Email {
				pairCount++
			}
		}
		if addressCount > addressLimit || pairCount > pairLimit {
			return ErrThrottled
		}
		return nil
	})
}
