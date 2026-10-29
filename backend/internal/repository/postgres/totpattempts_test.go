package postgres

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/totp"
)

func totpAttemptUser(t *testing.T, conn *pgx.Conn, email string) int64 {
	t.Helper()
	var id int64
	err := conn.QueryRow(t.Context(), "INSERT INTO users (email, password_hash, role) VALUES ($1, 'unused', 'member') RETURNING id", email).Scan(&id)
	require.NoError(t, err)
	require.Positive(t, id)
	return id
}

func TestTOTPAttemptsParallel(t *testing.T) {
	const requests = 16
	for _, c := range []struct {
		name            string
		seeded, allowed int
	}{
		{"short window", 0, 5},
		{"daily window", 18, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			db, conn := setupStorage(t)
			userID := totpAttemptUser(t, conn, "alice@example.com")
			other, err := Open(t.Context(), conn.Config().ConnString())
			require.NoError(t, err)
			t.Cleanup(other.Close)
			now := time.Date(2026, time.October, 29, 12, 0, 0, 0, time.UTC)
			_, err = conn.Exec(t.Context(), "INSERT INTO totp_attempts (user_id, created_at) SELECT $1, $2 FROM generate_series(1, $3)", userID, now.Add(-time.Hour), c.seeded)
			require.NoError(t, err)
			clock := func() time.Time { return now }
			logger := slog.New(slog.DiscardHandler)
			limiters := []*totp.Throttle{totp.NewThrottle(db, clock, logger), totp.NewThrottle(other, clock, logger)}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			start := make(chan struct{})
			results := make(chan error, requests)
			var group sync.WaitGroup
			for i := range requests {
				group.Go(func() {
					<-start
					_, err := limiters[i%len(limiters)].Begin(ctx, userID)
					results <- err
				})
			}
			close(start)
			group.Wait()
			close(results)
			var allowed, blocked int
			for err := range results {
				if errors.Is(err, totp.ErrThrottled) {
					blocked++
					continue
				}
				require.NoError(t, err)
				allowed++
			}
			assert.Equal(t, c.allowed, allowed)
			assert.Equal(t, requests-c.allowed, blocked)
			var count int
			require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM totp_attempts WHERE user_id = $1", userID).Scan(&count))
			assert.Equal(t, c.seeded+c.allowed, count, "blocked reservations must roll back")
		})
	}
}

func TestTOTPAttemptsSuccessExpiry(t *testing.T) {
	db, conn := setupStorage(t)
	alice := totpAttemptUser(t, conn, "alice@example.com")
	bob := totpAttemptUser(t, conn, "bob@example.com")
	now := time.Date(2026, time.October, 29, 12, 0, 0, 0, time.UTC)
	limiter := totp.NewThrottle(db, func() time.Time { return now }, slog.New(slog.DiscardHandler))
	aliceID, err := limiter.Begin(t.Context(), alice)
	require.NoError(t, err)
	require.Positive(t, aliceID)
	bobID, err := limiter.Begin(t.Context(), bob)
	require.NoError(t, err)
	require.Positive(t, bobID)
	require.NoError(t, limiter.Succeed(t.Context(), aliceID))
	require.NoError(t, limiter.Succeed(t.Context(), aliceID))
	var present bool
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM totp_attempts WHERE id = $1 AND user_id = $2)", bobID, bob).Scan(&present))
	assert.True(t, present, "success must not remove another user's attempt")
	aliceID, err = limiter.Begin(t.Context(), alice)
	require.NoError(t, err)
	require.Positive(t, aliceID)
	now = now.Add(24 * time.Hour)
	_, err = limiter.Begin(t.Context(), alice)
	require.NoError(t, err)
	var count int
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM totp_attempts WHERE user_id = $1", alice).Scan(&count))
	assert.Equal(t, 1, count, "the new reservation replaces expired rows at the exact cutoff")
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM totp_attempts WHERE user_id = $1", bob).Scan(&count))
	assert.Equal(t, 1, count, "cleanup is scoped to the user whose reservation is locked")
	require.NoError(t, limiter.Succeed(t.Context(), aliceID), "cleanup may have already removed a successful attempt")
}

func TestTOTPAttemptsRollback(t *testing.T) {
	db, conn := setupStorage(t)
	userID := totpAttemptUser(t, conn, "alice@example.com")
	now := time.Date(2026, time.October, 29, 12, 0, 0, 0, time.UTC)
	_, err := conn.Exec(t.Context(), "INSERT INTO totp_attempts (user_id, created_at) VALUES ($1, $2)", userID, now.Add(-48*time.Hour))
	require.NoError(t, err)
	rejected := errors.New("reservation rejected")
	id, err := db.ReserveTOTPAttempt(t.Context(), totp.Attempt{UserID: userID, CreatedAt: now}, now.Add(-24*time.Hour), func(rows []totp.Attempt) error {
		require.Len(t, rows, 1, "cleanup and insertion must precede the check")
		assert.Equal(t, userID, rows[0].UserID)
		assert.Equal(t, now, rows[0].CreatedAt)
		return rejected
	})
	require.ErrorIs(t, err, rejected)
	assert.Zero(t, id)
	var count int
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM totp_attempts WHERE user_id = $1 AND created_at = $2", userID, now.Add(-48*time.Hour)).Scan(&count))
	assert.Equal(t, 1, count, "rejection rolls back cleanup too")
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM totp_attempts WHERE user_id = $1", userID).Scan(&count))
	assert.Equal(t, 1, count, "the rejected reservation must not survive")
}

func TestTOTPAttemptsUnknownUser(t *testing.T) {
	db, _ := setupStorage(t)
	id, err := db.ReserveTOTPAttempt(t.Context(), totp.Attempt{UserID: 123, CreatedAt: time.Now()}, time.Now().Add(-24*time.Hour), func([]totp.Attempt) error {
		t.Fatal("missing users must not reach the check")
		return nil
	})
	require.ErrorIs(t, err, account.ErrUserNotFound)
	assert.Zero(t, id)
}

func TestTOTPAttemptsCommitFailure(t *testing.T) {
	db, conn := setupStorage(t)
	userID := totpAttemptUser(t, conn, "alice@example.com")
	_, err := conn.Exec(t.Context(), `
  CREATE FUNCTION reject_totp_attempt() RETURNS trigger LANGUAGE plpgsql AS
  $$ BEGIN RAISE EXCEPTION 'forced attempt failure'; END $$;
  CREATE CONSTRAINT TRIGGER reject_totp_attempt AFTER INSERT ON totp_attempts
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_totp_attempt();`)
	require.NoError(t, err)
	limiter := totp.NewThrottle(db, time.Now, slog.New(slog.DiscardHandler))
	id, err := limiter.Begin(t.Context(), userID)
	require.ErrorContains(t, err, "forced attempt failure")
	assert.Zero(t, id)
	var count int
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM totp_attempts").Scan(&count))
	assert.Zero(t, count)
}

func TestTOTPAttemptsMigrationDown(t *testing.T) {
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	defer conn.Close(t.Context())
	_, err = provider.DownTo(t.Context(), 5)
	require.NoError(t, err)
	var present bool
	const exists = "SELECT to_regclass('public.totp_attempts') IS NOT NULL"
	require.NoError(t, conn.QueryRow(t.Context(), exists).Scan(&present))
	assert.False(t, present)
	_, err = provider.Up(t.Context())
	require.NoError(t, err)
	require.NoError(t, conn.QueryRow(t.Context(), exists).Scan(&present))
	assert.True(t, present)
}
