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

// ReserveTOTPAttempt saves attempt, in one transaction, if check accepts its
// user's attempts made after cutoff, and returns its ID. It returns
// [account.ErrUserNotFound] when the user does not exist. An error from check
// is returned unchanged and saves nothing. Code verification happens after
// this transaction ends.
func (db *DB) ReserveTOTPAttempt(ctx context.Context, attempt totp.Attempt, cutoff time.Time, check func([]totp.Attempt) error) (int64, error) {
	var id int64
	err := pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Lock the user. The lock covers the count and the insert. A
		// deactivated owner still reserves an attempt, so the code step
		// reaches CompleteSignIn, which deletes their challenge and says the
		// account is deactivated.
		if _, err := queries.LockTOTPUser(ctx, attempt.UserID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return account.ErrUserNotFound
			}
			return fmt.Errorf("lock TOTP attempt owner: %w", err)
		}

		// 2. Delete attempts older than the window, then read the rest.
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

		// 3. Let check accept or refuse.
		if err := check(attempts); err != nil {
			return err
		}

		// 4. Save the attempt.
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
