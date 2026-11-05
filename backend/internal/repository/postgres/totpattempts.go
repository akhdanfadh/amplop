package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"inbok/internal/account"
	"inbok/internal/repository/postgres/sqlc"
	"inbok/internal/totp"
)

var _ totp.AttemptStorage = (*DB)(nil)

// ReserveTOTPAttempt locks the user, cleans expired rows, counts the rest, and
// inserts if check accepts. A rejected check rolls back; code verification
// happens afterward.
func (db *DB) ReserveTOTPAttempt(ctx context.Context, attempt totp.Attempt, cutoff time.Time, check func([]totp.Attempt) error) (int64, error) {
	var id int64
	err := pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		// A deactivated owner still reserves an attempt, so the code step
		// reaches CompleteSignIn, which deletes their challenge and says the
		// account is deactivated.
		if _, err := queries.LockTOTPUser(ctx, attempt.UserID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return account.ErrUserNotFound
			}
			return fmt.Errorf("lock TOTP attempt owner: %w", err)
		}
		if err := queries.DeleteOldTOTPAttempts(ctx, sqlc.DeleteOldTOTPAttemptsParams{UserID: attempt.UserID, Cutoff: cutoff}); err != nil {
			return fmt.Errorf("delete old TOTP attempts: %w", err)
		}
		rows, err := queries.ListTOTPAttempts(ctx, attempt.UserID)
		if err != nil {
			return fmt.Errorf("read TOTP attempts: %w", err)
		}
		attempts := make([]totp.Attempt, 0, len(rows))
		for _, row := range rows {
			attempts = append(attempts, totp.Attempt{UserID: row.UserID, CreatedAt: row.CreatedAt.UTC()})
		}
		if err := check(attempts); err != nil {
			return err
		}
		id, err = queries.InsertTOTPAttempt(ctx, sqlc.InsertTOTPAttemptParams{UserID: attempt.UserID, CreatedAt: attempt.CreatedAt})
		if err != nil {
			return fmt.Errorf("reserve TOTP attempt: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// DeleteTOTPAttempt removes one reservation by ID, after success or on
// release; absence is harmless.
func (db *DB) DeleteTOTPAttempt(ctx context.Context, id int64) error {
	if err := sqlc.New(db.pool).DeleteTOTPAttempt(ctx, id); err != nil {
		return fmt.Errorf("delete TOTP attempt: %w", err)
	}
	return nil
}
