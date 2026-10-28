package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"inbok/internal/repository/postgres/sqlc"
	"inbok/internal/session"
)

// Compile-time check that *DB implements session.AttemptStorage.
var _ session.AttemptStorage = (*DB)(nil)

// ReserveAttempt stores an attempt if check accepts the address's failure counts.
// The address lock covers reservation and counting, including the first attempt.
// Password verification happens after this transaction ends.
func (db *DB) ReserveAttempt(ctx context.Context, attempt session.Attempt, cutoff time.Time, check func([]session.Attempt) error) (int64, error) {
	var id int64
	err := pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		if err := queries.LockAttemptAddress(ctx, attempt.ClientAddr); err != nil {
			return fmt.Errorf("lock attempt address: %w", err)
		}
		if err := queries.DeleteOldSignInAttempts(ctx, cutoff); err != nil {
			return fmt.Errorf("delete old attempts: %w", err)
		}
		var err error
		id, err = queries.InsertSignInAttempt(ctx, sqlc.InsertSignInAttemptParams{
			Email: attempt.Email, ClientAddr: attempt.ClientAddr, CreatedAt: attempt.CreatedAt,
		})
		if err != nil {
			return fmt.Errorf("reserve attempt: %w", err)
		}
		rows, err := queries.ListSignInAttempts(ctx, attempt.ClientAddr)
		if err != nil {
			return fmt.Errorf("read attempts: %w", err)
		}
		attempts := make([]session.Attempt, 0, len(rows))
		for _, row := range rows {
			attempts = append(attempts, session.Attempt{Email: row.Email, ClientAddr: row.ClientAddr, CreatedAt: row.CreatedAt.UTC()})
		}
		return check(attempts)
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
