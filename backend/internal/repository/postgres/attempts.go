package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"inbok/internal/repository/postgres/sqlc"
	"inbok/internal/session"
)

// ReserveAttempt saves attempt, in one transaction, if check accepts the
// attempts from its address made after cutoff, and returns its ID. An error
// from check is returned unchanged and saves nothing. Password verification
// happens after this transaction ends.
func (db *DB) ReserveAttempt(ctx context.Context, attempt session.Attempt, cutoff time.Time, check func([]session.Attempt) error) (int64, error) {
	var id int64
	err := pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Lock the address. The lock covers the count and the insert,
		// including the address's first attempt.
		if err := queries.LockAttemptClient(ctx, attempt.ClientPrefix); err != nil {
			return fmt.Errorf("lock attempt address: %w", err)
		}

		// 2. Delete attempts older than the window, then read the rest.
		if err := queries.DeleteOldSignInAttempts(ctx, cutoff); err != nil {
			return fmt.Errorf("delete old attempts: %w", err)
		}
		rows, err := queries.ListSignInAttempts(ctx, attempt.ClientPrefix)
		if err != nil {
			return fmt.Errorf("read attempts: %w", err)
		}
		attempts := make([]session.Attempt, 0, len(rows))
		for _, row := range rows {
			attempts = append(attempts, session.Attempt{Email: row.Email, ClientPrefix: row.ClientPrefix, CreatedAt: row.CreatedAt.UTC()})
		}

		// 3. Let check accept or refuse.
		if err := check(attempts); err != nil {
			return err
		}

		// 4. Save the attempt.
		id, err = queries.InsertSignInAttempt(ctx, sqlc.InsertSignInAttemptParams{
			Email: attempt.Email, ClientPrefix: attempt.ClientPrefix, CreatedAt: attempt.CreatedAt,
		})
		if err != nil {
			return fmt.Errorf("reserve attempt: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// DeleteAttempt removes an attempt by ID; missing rows are harmless.
func (db *DB) DeleteAttempt(ctx context.Context, id int64) error {
	if err := sqlc.New(db.pool).DeleteSignInAttempt(ctx, id); err != nil {
		return fmt.Errorf("delete attempt: %w", err)
	}
	return nil
}
