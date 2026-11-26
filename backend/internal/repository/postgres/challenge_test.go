package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/repository/postgres/sqlc"
	"amplop/internal/session"
)

func challengeStorage(t *testing.T) (*DB, *pgx.Conn, session.Challenge) {
	t.Helper()
	db, conn := setupStorage(t)
	state := session.Challenge{
		TokenHash: sha256.Sum256([]byte("test-only-challenge-secret")),
		ExpiresAt: time.Date(2026, time.October, 29, 12, 5, 0, 0, time.UTC),
	}
	err := conn.QueryRow(t.Context(), "INSERT INTO users (email, password_hash, role) VALUES ('alice@example.com', 'unused', 'member') RETURNING id").Scan(&state.UserID)
	require.NoError(t, err)
	require.NoError(t, sqlc.New(conn).InsertSignInChallenge(t.Context(), sqlc.InsertSignInChallengeParams{
		TokenHash: state.TokenHash[:], UserID: state.UserID, ExpiresAt: state.ExpiresAt,
	}))
	return db, conn, state
}

func TestChallengeStorage(t *testing.T) {
	_, conn, state := challengeStorage(t)
	queries := sqlc.New(conn)
	key := sqlc.LockSignInChallengeParams{UserID: state.UserID, TokenHash: state.TokenHash[:]}

	owner, err := queries.ReadSignInChallengeUser(t.Context(), state.TokenHash[:])
	require.NoError(t, err)
	assert.Equal(t, state.UserID, owner)
	row, err := queries.LockSignInChallenge(t.Context(), key)
	require.NoError(t, err)
	assert.Equal(t, state.UserID, row.UserID)
	assert.Zero(t, row.Failures)
	assert.WithinDuration(t, state.ExpiresAt, row.ExpiresAt, time.Microsecond)

	// Neither reading nor changing a challenge may cross its owner boundary.
	_, err = queries.LockSignInChallenge(t.Context(), sqlc.LockSignInChallengeParams{UserID: state.UserID + 1, TokenHash: state.TokenHash[:]})
	require.ErrorIs(t, err, pgx.ErrNoRows)
	require.NoError(t, queries.UpdateSignInChallengeFailures(t.Context(), sqlc.UpdateSignInChallengeFailuresParams{
		UserID: state.UserID + 1, TokenHash: state.TokenHash[:], Failures: 4,
	}))
	row, err = queries.LockSignInChallenge(t.Context(), key)
	require.NoError(t, err)
	assert.Zero(t, row.Failures)

	// A failed code changes only the counter, not the fixed expiry.
	require.NoError(t, queries.UpdateSignInChallengeFailures(t.Context(), sqlc.UpdateSignInChallengeFailuresParams{
		UserID: state.UserID, TokenHash: state.TokenHash[:], Failures: 1,
	}))
	row, err = queries.LockSignInChallenge(t.Context(), key)
	require.NoError(t, err)
	assert.Equal(t, int32(1), row.Failures)
	assert.WithinDuration(t, state.ExpiresAt, row.ExpiresAt, time.Microsecond)

	// A challenge secret has no row in the authenticated session table.
	_, err = queries.LockSession(t.Context(), state.TokenHash[:])
	require.ErrorIs(t, err, pgx.ErrNoRows)
	_, err = queries.DeleteSignInChallenge(t.Context(), sqlc.DeleteSignInChallengeParams{UserID: state.UserID + 1, TokenHash: state.TokenHash[:]})
	require.ErrorIs(t, err, pgx.ErrNoRows)
	hash, err := queries.DeleteSignInChallenge(t.Context(), sqlc.DeleteSignInChallengeParams{UserID: state.UserID, TokenHash: state.TokenHash[:]})
	require.NoError(t, err)
	assert.Equal(t, state.TokenHash[:], hash)
	_, err = queries.DeleteSignInChallenge(t.Context(), sqlc.DeleteSignInChallengeParams{UserID: state.UserID, TokenHash: state.TokenHash[:]})
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestChallengeConstraints(t *testing.T) {
	_, conn, state := challengeStorage(t)
	queries := sqlc.New(conn)

	err := queries.UpdateSignInChallengeFailures(t.Context(), sqlc.UpdateSignInChallengeFailuresParams{
		UserID: state.UserID, TokenHash: state.TokenHash[:], Failures: -1,
	})
	var constraint *pgconn.PgError
	require.ErrorAs(t, err, &constraint)
	assert.Equal(t, "23514", constraint.Code)
	// Deleting an account removes its pending password proofs too.
	_, err = conn.Exec(t.Context(), "DELETE FROM users WHERE id = $1", state.UserID)
	require.NoError(t, err)
	_, err = queries.ReadSignInChallengeUser(t.Context(), state.TokenHash[:])
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestChallengeRollback(t *testing.T) {
	db, conn, state := challengeStorage(t)
	rejected := errors.New("protected action failed")

	err := pgx.BeginTxFunc(t.Context(), db.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		if _, err := queries.LockSignInChallenge(t.Context(), sqlc.LockSignInChallengeParams{UserID: state.UserID, TokenHash: state.TokenHash[:]}); err != nil {
			return err
		}
		if err := queries.UpdateSignInChallengeFailures(t.Context(), sqlc.UpdateSignInChallengeFailuresParams{
			UserID: state.UserID, TokenHash: state.TokenHash[:], Failures: 2,
		}); err != nil {
			return err
		}
		if _, err := queries.DeleteSignInChallenge(t.Context(), sqlc.DeleteSignInChallengeParams{UserID: state.UserID, TokenHash: state.TokenHash[:]}); err != nil {
			return err
		}
		return rejected
	})

	require.ErrorIs(t, err, rejected)
	row, err := sqlc.New(conn).LockSignInChallenge(t.Context(), sqlc.LockSignInChallengeParams{UserID: state.UserID, TokenHash: state.TokenHash[:]})
	require.NoError(t, err)
	assert.Zero(t, row.Failures)
	assert.WithinDuration(t, state.ExpiresAt, row.ExpiresAt, time.Microsecond)
}

func TestChallengeParallel(t *testing.T) {
	db, conn, state := challengeStorage(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	locked, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	consume := func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		_, err := queries.LockSignInChallenge(ctx, sqlc.LockSignInChallengeParams{UserID: state.UserID, TokenHash: state.TokenHash[:]})
		if err != nil {
			return err
		}
		_, err = queries.DeleteSignInChallenge(ctx, sqlc.DeleteSignInChallengeParams{UserID: state.UserID, TokenHash: state.TokenHash[:]})
		return err
	}

	go func() {
		firstDone <- pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := sqlc.New(tx).LockSignInChallenge(ctx, sqlc.LockSignInChallengeParams{UserID: state.UserID, TokenHash: state.TokenHash[:]}); err != nil {
				return err
			}
			close(locked)
			select {
			case <-release:
				return consume(tx)
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-locked:
	case <-ctx.Done():
		require.FailNow(t, "first consumer did not lock challenge")
	}
	go func() { secondDone <- pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{}, consume) }()

	// Prove the second consumer waits before the first commits deletion.
	require.Eventually(t, func() bool {
		var blocked bool
		err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%LockSignInChallenge%')").Scan(&blocked)
		return err == nil && blocked
	}, 3*time.Second, 10*time.Millisecond)
	unblock()
	require.NoError(t, <-firstDone)
	require.ErrorIs(t, <-secondDone, pgx.ErrNoRows)
}

func TestChallengePruning(t *testing.T) {
	_, conn, state := challengeStorage(t)
	queries := sqlc.New(conn)
	var otherID int64
	require.NoError(t, conn.QueryRow(t.Context(), "INSERT INTO users (email, password_hash, role) VALUES ('bob@example.com', 'unused', 'member') RETURNING id").Scan(&otherID))
	liveHash, otherHash := sha256.Sum256([]byte("live-proof")), sha256.Sum256([]byte("other-proof"))
	for _, row := range []sqlc.InsertSignInChallengeParams{
		{UserID: state.UserID, TokenHash: liveHash[:], ExpiresAt: state.ExpiresAt.Add(time.Microsecond)},
		{UserID: otherID, TokenHash: otherHash[:], ExpiresAt: state.ExpiresAt},
	} {
		require.NoError(t, queries.InsertSignInChallenge(t.Context(), row))
	}

	require.NoError(t, queries.DeleteExpiredSignInChallenges(t.Context(), sqlc.DeleteExpiredSignInChallengesParams{UserID: state.UserID, Cutoff: state.ExpiresAt}))

	_, err := queries.ReadSignInChallengeUser(t.Context(), state.TokenHash[:])
	require.ErrorIs(t, err, pgx.ErrNoRows)
	owner, err := queries.ReadSignInChallengeUser(t.Context(), liveHash[:])
	require.NoError(t, err)
	assert.Equal(t, state.UserID, owner)
	owner, err = queries.ReadSignInChallengeUser(t.Context(), otherHash[:])
	require.NoError(t, err)
	assert.Equal(t, otherID, owner)
}

func TestChallengeMigrationDown(t *testing.T) {
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	defer conn.Close(t.Context())
	_, err = provider.DownTo(t.Context(), 8)
	require.NoError(t, err)
	var present bool
	const exists = "SELECT to_regclass('public.sign_in_challenges') IS NOT NULL"
	require.NoError(t, conn.QueryRow(t.Context(), exists).Scan(&present))
	assert.False(t, present)
	_, err = provider.Up(t.Context())
	require.NoError(t, err)
	require.NoError(t, conn.QueryRow(t.Context(), exists).Scan(&present))
	assert.True(t, present)
}
