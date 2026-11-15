package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/repository/postgres/sqlc"
	"inbok/internal/totp"
)

func backupCodeUser(t *testing.T, conn *pgx.Conn, email string) int64 {
	t.Helper()
	var id int64
	err := conn.QueryRow(t.Context(), "INSERT INTO users (email, password_hash, role) VALUES ($1, 'unused', 'member') RETURNING id", email).Scan(&id)
	require.NoError(t, err)
	_, err = conn.Exec(t.Context(), "INSERT INTO user_totp (user_id, key_id, secret_ciphertext, confirmed_at, started_at) VALUES ($1, '1', 'ciphertext', now(), now())", id)
	require.NoError(t, err)
	return id
}

func TestBackupCodeStorage(t *testing.T) {
	_, conn := setupStorage(t)
	alice := backupCodeUser(t, conn, "alice@example.com")
	bob := backupCodeUser(t, conn, "bob@example.com")
	queries := sqlc.New(conn)
	set, err := totp.NewBackupCodes(t.Context())
	require.NoError(t, err)
	for _, hash := range set.Hashes {
		require.NoError(t, queries.InsertTOTPBackupCode(t.Context(), sqlc.InsertTOTPBackupCodeParams{UserID: alice, CodeHash: hash}))
	}
	rows, err := queries.ReadTOTPBackupCodes(t.Context(), alice)
	require.NoError(t, err)
	require.Len(t, rows, len(set.Hashes))
	for i, row := range rows {
		require.Positive(t, row.ID)
		assert.Equal(t, set.Hashes[i], row.CodeHash)
		require.NoError(t, totp.VerifyBackupCode(t.Context(), set.Codes[i], row.CodeHash))
	}
	other, err := queries.ReadTOTPBackupCodes(t.Context(), bob)
	require.NoError(t, err)
	assert.Empty(t, other)

	// A row ID alone cannot consume another user's code.
	id, err := queries.ConsumeTOTPBackupCode(t.Context(), sqlc.ConsumeTOTPBackupCodeParams{UserID: bob, ID: rows[0].ID})
	require.ErrorIs(t, err, pgx.ErrNoRows)
	assert.Zero(t, id)
	id, err = queries.ConsumeTOTPBackupCode(t.Context(), sqlc.ConsumeTOTPBackupCodeParams{UserID: alice, ID: rows[0].ID})
	require.NoError(t, err)
	assert.Equal(t, rows[0].ID, id)
	_, err = queries.ConsumeTOTPBackupCode(t.Context(), sqlc.ConsumeTOTPBackupCodeParams{UserID: alice, ID: rows[0].ID})
	require.ErrorIs(t, err, pgx.ErrNoRows)
	remaining, err := queries.ReadTOTPBackupCodes(t.Context(), alice)
	require.NoError(t, err)
	assert.Len(t, remaining, len(rows)-1)

	// Removing the factor also removes its backup codes.
	_, err = conn.Exec(t.Context(), "DELETE FROM user_totp WHERE user_id = $1", alice)
	require.NoError(t, err)
	remaining, err = queries.ReadTOTPBackupCodes(t.Context(), alice)
	require.NoError(t, err)
	assert.Empty(t, remaining)
}

func TestBackupCodeTransaction(t *testing.T) {
	db, conn := setupStorage(t)
	userID := backupCodeUser(t, conn, "alice@example.com")
	queries := sqlc.New(conn)
	require.NoError(t, queries.InsertTOTPBackupCode(t.Context(), sqlc.InsertTOTPBackupCodeParams{UserID: userID, CodeHash: "stored-hash"}))
	rows, err := queries.ReadTOTPBackupCodes(t.Context(), userID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	failed := errors.New("protected action failed")
	for _, actionErr := range []error{failed, nil} {
		err := pgx.BeginTxFunc(t.Context(), db.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			_, err := sqlc.New(tx).ConsumeTOTPBackupCode(t.Context(), sqlc.ConsumeTOTPBackupCodeParams{UserID: userID, ID: rows[0].ID})
			if err != nil {
				return err
			}
			// Stand in for the action that must commit with consumption.
			_, err = tx.Exec(t.Context(), "UPDATE user_totp SET last_step = 12 WHERE user_id = $1", userID)
			if err != nil {
				return err
			}
			return actionErr
		})
		if actionErr != nil {
			require.ErrorIs(t, err, failed)
		} else {
			require.NoError(t, err)
		}
		remaining, err := queries.ReadTOTPBackupCodes(t.Context(), userID)
		require.NoError(t, err)
		var step int64
		require.NoError(t, conn.QueryRow(t.Context(), "SELECT last_step FROM user_totp WHERE user_id = $1", userID).Scan(&step))
		if actionErr != nil {
			assert.Equal(t, rows, remaining, "rollback must leave the code usable")
			assert.Equal(t, int64(-1), step)
			continue
		}
		assert.Empty(t, remaining)
		assert.Equal(t, int64(12), step)
	}
}

func TestBackupCodeParallel(t *testing.T) {
	db, conn := setupStorage(t)
	userID := backupCodeUser(t, conn, "alice@example.com")
	queries := sqlc.New(conn)
	require.NoError(t, queries.InsertTOTPBackupCode(t.Context(), sqlc.InsertTOTPBackupCodeParams{UserID: userID, CodeHash: "stored-hash"}))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ready, start := make(chan error, 2), make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			results <- pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
				q := sqlc.New(tx)
				rows, err := q.ReadTOTPBackupCodes(ctx, userID)
				if err == nil && len(rows) != 1 {
					err = errors.New("expected one backup-code candidate")
				}
				ready <- err
				if err != nil {
					return err
				}
				select {
				case <-start:
				case <-ctx.Done():
					return ctx.Err()
				}
				_, err = q.ConsumeTOTPBackupCode(ctx, sqlc.ConsumeTOTPBackupCodeParams{UserID: userID, ID: rows[0].ID})
				return err
			})
		}()
	}
	// Both transactions have the same candidate before either can consume it.
	for range 2 {
		select {
		case err := <-ready:
			require.NoError(t, err)
		case <-ctx.Done():
			require.FailNow(t, "both consumers did not read the candidate")
		}
	}
	close(start)
	var accepted, rejected int
	for range 2 {
		err := <-results
		if errors.Is(err, pgx.ErrNoRows) {
			rejected++
			continue
		}
		require.NoError(t, err)
		accepted++
	}
	assert.Equal(t, 1, accepted)
	assert.Equal(t, 1, rejected)
}

func TestBackupCodeMigrationDown(t *testing.T) {
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	defer conn.Close(t.Context())
	_, err = provider.DownTo(t.Context(), 6)
	require.NoError(t, err)
	var present bool
	const exists = "SELECT to_regclass('public.totp_backup_codes') IS NOT NULL"
	require.NoError(t, conn.QueryRow(t.Context(), exists).Scan(&present))
	assert.False(t, present)
	_, err = provider.Up(t.Context())
	require.NoError(t, err)
	require.NoError(t, conn.QueryRow(t.Context(), exists).Scan(&present))
	assert.True(t, present)
}
