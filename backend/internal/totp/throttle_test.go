package totp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/attemptlimit"
	"amplop/internal/logging"
)

type memoryCodeAttempts struct {
	mu         sync.Mutex
	next       int64
	rows       map[int64]Attempt
	reserveErr error
	deleteErr  error
}

func (m *memoryCodeAttempts) ReserveTOTPAttempt(_ context.Context, attempt Attempt, cutoff time.Time, check func([]Attempt) error) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reserveErr != nil {
		return 0, m.reserveErr
	}
	next := make(map[int64]Attempt)
	for id, row := range m.rows {
		if row.CreatedAt.After(cutoff) {
			next[id] = row
		}
	}
	var rows []Attempt
	for _, row := range next {
		if row.UserID == attempt.UserID {
			rows = append(rows, row)
		}
	}
	if err := check(rows); err != nil {
		return 0, err
	}
	m.next++
	next[m.next] = attempt
	m.rows = next
	return m.next, nil
}

func (m *memoryCodeAttempts) DeleteTOTPAttempt(ctx context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.rows, id)
	return nil
}

func TestThrottleShortWindow(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, time.October, 29, 12, 0, 0, 0, time.UTC)}
	store := &memoryCodeAttempts{}
	limiter := NewThrottle(store, clock.Now, slog.New(slog.DiscardHandler))
	for range 5 {
		_, err := limiter.Begin(t.Context(), 42)
		require.NoError(t, err)
	}
	_, err := limiter.Begin(t.Context(), 42)
	require.ErrorIs(t, err, ErrThrottled)
	_, err = limiter.Begin(t.Context(), 43)
	require.NoError(t, err, "another user's attempts must not count")
	clock.Advance(15*time.Minute - time.Nanosecond)
	_, err = limiter.Begin(t.Context(), 42)
	require.ErrorIs(t, err, ErrThrottled)
	clock.Advance(time.Nanosecond)
	_, err = limiter.Begin(t.Context(), 42)
	require.NoError(t, err, "attempts exactly at the cutoff no longer count")
}

func TestThrottleLongWindow(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, time.October, 29, 12, 0, 0, 0, time.UTC)}
	start := clock.now
	limiter := NewThrottle(&memoryCodeAttempts{}, clock.Now, slog.New(slog.DiscardHandler))
	for range 4 {
		for range 5 {
			_, err := limiter.Begin(t.Context(), 42)
			require.NoError(t, err)
		}
		clock.Advance(15 * time.Minute)
	}
	_, err := limiter.Begin(t.Context(), 42)
	require.ErrorIs(t, err, ErrThrottled, "the daily limit still applies after the short window expires")
	refusal, ok := errors.AsType[*attemptlimit.Error](err)
	require.True(t, ok)
	assert.Equal(t, start.Add(24*time.Hour).Sub(clock.now), refusal.Wait, "the wait runs until the first attempt leaves the daily window")
	clock.now = start.Add(24*time.Hour - time.Nanosecond)
	_, err = limiter.Begin(t.Context(), 42)
	require.ErrorIs(t, err, ErrThrottled)
	clock.Advance(time.Nanosecond)
	_, err = limiter.Begin(t.Context(), 42)
	require.NoError(t, err)
}

func TestThrottleSuccess(t *testing.T) {
	store := &memoryCodeAttempts{}
	limiter := NewThrottle(store, time.Now, slog.New(slog.DiscardHandler))
	for range 4 {
		_, err := limiter.Begin(t.Context(), 42)
		require.NoError(t, err)
	}
	id, err := limiter.Begin(t.Context(), 42)
	require.NoError(t, err)
	require.Positive(t, id)
	require.NoError(t, limiter.Succeed(t.Context(), id))
	require.NoError(t, limiter.Succeed(t.Context(), id), "removing an already absent attempt is harmless")
	assert.Len(t, store.rows, 4, "success must not erase earlier failed attempts")
	_, err = limiter.Begin(t.Context(), 42)
	require.NoError(t, err)
	_, err = limiter.Begin(t.Context(), 42)
	require.ErrorIs(t, err, ErrThrottled)
}

func TestThrottleParallel(t *testing.T) {
	const requests = 16
	store := &memoryCodeAttempts{}
	now := time.Now()
	clock := func() time.Time { return now }
	logger := slog.New(slog.DiscardHandler)
	limiters := []*Throttle{NewThrottle(store, clock, logger), NewThrottle(store, clock, logger)}
	start := make(chan struct{})
	results := make(chan error, requests)
	var group sync.WaitGroup
	for i := range requests {
		group.Go(func() {
			<-start
			_, err := limiters[i%len(limiters)].Begin(t.Context(), 42)
			results <- err
		})
	}
	close(start)
	group.Wait()
	close(results)
	var allowed, blocked int
	for err := range results {
		if errors.Is(err, ErrThrottled) {
			blocked++
			continue
		}
		require.NoError(t, err)
		allowed++
	}
	assert.Equal(t, 5, allowed)
	assert.Equal(t, 11, blocked)
	assert.Len(t, store.rows, 5, "rejected reservations must not extend the lockout")
}

func TestThrottleWarning(t *testing.T) {
	var logs bytes.Buffer
	limiter := NewThrottle(&memoryCodeAttempts{}, time.Now, logging.New(&logs))
	for range 5 {
		_, err := limiter.Begin(t.Context(), 42)
		require.NoError(t, err)
	}
	assert.Empty(t, logs.String(), "allowed attempts need no warning")
	_, err := limiter.Begin(logging.WithRequestID(t.Context(), "req-1"), 42)
	require.ErrorIs(t, err, ErrThrottled)
	assert.Contains(t, logs.String(), "level=WARN")
	assert.Contains(t, logs.String(), "user_id=42")
	assert.Contains(t, logs.String(), "request_id=req-1")
	assert.NotContains(t, logs.String(), "code=")
	assert.NotContains(t, logs.String(), "secret=")
}

func TestThrottleStorageFailure(t *testing.T) {
	failure := errors.New("storage unavailable")
	store := &memoryCodeAttempts{reserveErr: failure}
	var logs bytes.Buffer
	limiter := NewThrottle(store, time.Now, slog.New(slog.NewTextHandler(&logs, nil)))
	id, err := limiter.Begin(t.Context(), 42)
	require.ErrorIs(t, err, failure)
	assert.Zero(t, id)
	assert.Empty(t, logs.String())
	store.reserveErr, store.deleteErr = nil, failure
	id, err = limiter.Begin(t.Context(), 42)
	require.NoError(t, err)
	require.Positive(t, id)
	err = limiter.Succeed(t.Context(), id)
	require.ErrorIs(t, err, failure)
	assert.Len(t, store.rows, 1, "failed removal must keep the reservation")
}

func TestThrottleInvalidUser(t *testing.T) {
	store := &memoryCodeAttempts{}
	limiter := NewThrottle(store, time.Now, slog.New(slog.DiscardHandler))
	for _, userID := range []int64{0, -1} {
		id, err := limiter.Begin(t.Context(), userID)
		require.ErrorIs(t, err, errAttemptUserID)
		assert.Zero(t, id)
	}
	assert.Empty(t, store.rows)
}

// [Throttle.Release] removes an attempt so it no longer counts, even after the
// request was cancelled.
func TestThrottleRelease(t *testing.T) {
	store := &memoryCodeAttempts{}
	limiter := NewThrottle(store, time.Now, slog.New(slog.DiscardHandler))
	var id int64
	for range 5 {
		var err error
		id, err = limiter.Begin(t.Context(), 42)
		require.NoError(t, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	limiter.Release(ctx, id)

	_, err := limiter.Begin(t.Context(), 42)
	require.NoError(t, err)
}

// A release that fails is logged, and the attempt keeps counting.
func TestThrottleReleaseFailure(t *testing.T) {
	store := &memoryCodeAttempts{}
	var logs bytes.Buffer
	limiter := NewThrottle(store, time.Now, logging.New(&logs))
	var id int64
	for range 5 {
		var err error
		id, err = limiter.Begin(t.Context(), 42)
		require.NoError(t, err)
	}
	store.deleteErr = errors.New("storage unavailable")

	limiter.Release(logging.WithRequestID(t.Context(), "req-1"), id)

	assert.Contains(t, logs.String(), "level=WARN")
	assert.Contains(t, logs.String(), "request_id=req-1")
	assert.Contains(t, logs.String(), fmt.Sprintf("attempt_id=%d", id))
	_, err := limiter.Begin(t.Context(), 42)
	require.ErrorIs(t, err, ErrThrottled)
}
