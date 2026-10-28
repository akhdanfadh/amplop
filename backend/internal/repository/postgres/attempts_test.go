package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/session"
)

func TestAttemptsParallel(t *testing.T) {
	cases := []struct {
		name              string
		requests, allowed int
		email             func(int) string
	}{
		{"pair", 16, 5, func(int) string { return "Admin@example.com" }},
		{"address", 64, 50, func(i int) string { return fmt.Sprintf("user%d@example.com", i) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db, conn := setupStorage(t)
			other, err := Open(t.Context(), conn.Config().ConnString())
			require.NoError(t, err)
			t.Cleanup(other.Close)
			now := time.Date(2026, time.October, 28, 12, 0, 0, 0, time.UTC)
			clock := func() time.Time { return now }
			limiters := []*session.Throttle{session.NewThrottle(db, clock), session.NewThrottle(other, clock)}
			start := make(chan struct{})
			errs := make(chan error, c.requests)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			var group sync.WaitGroup
			for i := range c.requests {
				group.Go(func() {
					<-start
					_, err := limiters[i%len(limiters)].Begin(ctx, c.email(i), netip.MustParseAddr("192.0.2.1"))
					errs <- err
				})
			}
			close(start)
			group.Wait()
			close(errs)
			var allowed, blocked int
			for err := range errs {
				if errors.Is(err, session.ErrThrottled) {
					blocked++
					continue
				}
				require.NoError(t, err)
				allowed++
			}
			assert.Equal(t, c.allowed, allowed)
			assert.Equal(t, c.requests-c.allowed, blocked)
			var count int
			require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM sign_in_attempts").Scan(&count))
			assert.Equal(t, c.allowed, count)
		})
	}
}

func TestAttemptsSuccessAndExpiry(t *testing.T) {
	db, conn := setupStorage(t)
	now := time.Date(2026, time.October, 28, 12, 0, 0, 0, time.UTC)
	limiter := session.NewThrottle(db, func() time.Time { return now })
	addr := netip.MustParseAddr("192.0.2.1")
	id, err := limiter.Begin(t.Context(), "Admin@example.com", addr)
	require.NoError(t, err)
	_, err = limiter.BeginSetup(t.Context(), addr)
	require.NoError(t, err)
	require.NoError(t, limiter.Succeed(t.Context(), id))
	require.NoError(t, limiter.Succeed(t.Context(), id))
	var count int
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM sign_in_attempts").Scan(&count))
	assert.Equal(t, 1, count)

	now = now.Add(15 * time.Minute)
	_, err = limiter.Begin(t.Context(), "a@example.com", netip.MustParseAddr("192.0.2.2"))
	require.NoError(t, err)
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM sign_in_attempts").Scan(&count))
	assert.Equal(t, 1, count, "old rows from other addresses must be removed too")
}

func TestAttemptsRollback(t *testing.T) {
	db, conn := setupStorage(t)
	now := time.Date(2026, time.October, 28, 12, 0, 0, 0, time.UTC)
	attempt := session.Attempt{ClientAddr: "192.0.2.1", CreatedAt: now}
	rejected := errors.New("reservation rejected")
	id, err := db.ReserveAttempt(t.Context(), attempt, now.Add(-15*time.Minute), func(rows []session.Attempt) error {
		assert.Len(t, rows, 1, "the reservation must exist before counting")
		return rejected
	})
	require.ErrorIs(t, err, rejected)
	assert.Zero(t, id)
	var count int
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM sign_in_attempts").Scan(&count))
	assert.Zero(t, count)
}
