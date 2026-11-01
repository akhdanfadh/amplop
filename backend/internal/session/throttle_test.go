package session

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/attemptlimit"
	"inbok/internal/emailaddress"
)

type memoryAttempts struct {
	mu   sync.Mutex
	next int64
	rows map[int64]Attempt
}

func (s *memoryAttempts) ReserveAttempt(_ context.Context, attempt Attempt, cutoff time.Time, check func([]Attempt) error) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, row := range s.rows {
		if !row.CreatedAt.After(cutoff) {
			delete(s.rows, id)
		}
	}
	var rows []Attempt
	for _, row := range s.rows {
		if row.ClientAddr == attempt.ClientAddr {
			rows = append(rows, row)
		}
	}
	if err := check(rows); err != nil {
		return 0, err
	}
	s.next++
	s.rows[s.next] = attempt
	return s.next, nil
}

func (s *memoryAttempts) DeleteAttempt(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, id)
	return nil
}

func TestThrottlePair(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, time.October, 28, 12, 0, 0, 0, time.UTC)}
	store := &memoryAttempts{rows: map[int64]Attempt{}}
	limiter := NewThrottle(store, clock.Now)
	addr := netip.MustParseAddr("192.0.2.1")
	for range 5 {
		_, err := limiter.Begin(t.Context(), "Admin@Example.COM", addr)
		require.NoError(t, err)
	}
	_, err := limiter.Begin(t.Context(), " admin@example.com ", addr)
	require.ErrorIs(t, err, ErrThrottled)
	refusal, ok := errors.AsType[*attemptlimit.Error](err)
	require.True(t, ok)
	assert.Equal(t, 15*time.Minute, refusal.Wait, "the wait runs until the oldest attempt leaves the window")
	_, err = limiter.Begin(t.Context(), "admin@example.com", netip.MustParseAddr("192.0.2.2"))
	require.NoError(t, err)
	clock.Advance(15*time.Minute - time.Nanosecond)
	_, err = limiter.Begin(t.Context(), "admin@example.com", addr)
	require.ErrorIs(t, err, ErrThrottled)
	clock.Advance(time.Nanosecond)
	_, err = limiter.Begin(t.Context(), "admin@example.com", addr)
	require.NoError(t, err)
	assert.Len(t, store.rows, 1)
}

// Throttle keys are addresses as emailaddress.Parse returns them.
func TestThrottleKey(t *testing.T) {
	store := &memoryAttempts{rows: map[int64]Attempt{}}
	limiter := NewThrottle(store, time.Now)

	id, err := limiter.Begin(t.Context(), " Admin@Example.COM\t", netip.MustParseAddr("192.0.2.1"))
	require.NoError(t, err)

	require.NotNil(t, store.rows[id].Email)
	assert.Equal(t, "admin@example.com", *store.rows[id].Email)
}

func TestThrottleInvalidEmail(t *testing.T) {
	store := &memoryAttempts{rows: map[int64]Attempt{}}
	limiter := NewThrottle(store, time.Now)

	_, err := limiter.Begin(t.Context(), "admin", netip.MustParseAddr("192.0.2.1"))

	require.ErrorIs(t, err, emailaddress.ErrInvalid)
	assert.Empty(t, store.rows, "an invalid email reserves nothing")
}

func TestThrottleSuccess(t *testing.T) {
	store := &memoryAttempts{rows: map[int64]Attempt{}}
	limiter := NewThrottle(store, time.Now)
	addr := netip.MustParseAddr("192.0.2.1")
	for range 4 {
		_, err := limiter.Begin(t.Context(), "a@example.com", addr)
		require.NoError(t, err)
	}
	id, err := limiter.Begin(t.Context(), "a@example.com", addr)
	require.NoError(t, err)
	require.NoError(t, limiter.Succeed(t.Context(), id))
	require.NoError(t, limiter.Succeed(t.Context(), id))
	assert.Len(t, store.rows, 4)
	_, err = limiter.Begin(t.Context(), "a@example.com", addr)
	require.NoError(t, err)
	_, err = limiter.Begin(t.Context(), "a@example.com", netip.MustParseAddr("::ffff:192.0.2.1"))
	require.ErrorIs(t, err, ErrThrottled)
}

func TestThrottleSetupSharesAddress(t *testing.T) {
	store := &memoryAttempts{rows: map[int64]Attempt{}}
	limiter := NewThrottle(store, time.Now)
	addr := netip.MustParseAddr("192.0.2.1")
	for range 5 {
		_, err := limiter.Begin(t.Context(), "a@example.com", addr)
		require.NoError(t, err)
	}
	var id int64
	for range 45 {
		var err error
		id, err = limiter.BeginSetup(t.Context(), addr)
		require.NoError(t, err)
	}
	_, err := limiter.BeginSetup(t.Context(), addr)
	require.ErrorIs(t, err, ErrThrottled)
	_, err = limiter.Begin(t.Context(), "another@example.com", addr)
	require.ErrorIs(t, err, ErrThrottled)
	require.NoError(t, limiter.Succeed(t.Context(), id))
	_, err = limiter.Begin(t.Context(), "another@example.com", addr)
	require.NoError(t, err)
	assert.Len(t, store.rows, 50)
}

func TestThrottleInvalidAddress(t *testing.T) {
	store := &memoryAttempts{rows: map[int64]Attempt{}}
	_, err := NewThrottle(store, time.Now).BeginSetup(t.Context(), netip.Addr{})
	require.EqualError(t, err, "invalid client address")
	assert.Empty(t, store.rows)
}
