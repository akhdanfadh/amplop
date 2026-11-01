package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"inbok/internal/account"
	"inbok/internal/repository/postgres/sqlc"
	"inbok/internal/totp"
)

// DisableTOTP checks the locked owner's current role before removing a factor.
func (db *DB) DisableTOTP(ctx context.Context, userID, attemptID int64, check func(totp.DisableState) error) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		owner, err := queries.LockTOTPUserRole(ctx, userID)
		// A deactivation ended the session this request was checked with.
		if errors.Is(err, pgx.ErrNoRows) || owner.Deactivated {
			return account.ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("lock TOTP turn-off owner: %w", err)
		}

		factor, err := readTOTP(ctx, queries, userID)
		if err != nil {
			return err
		}
		rows, err := queries.ReadTOTPBackupCodes(ctx, userID)
		if err != nil {
			return fmt.Errorf("read turn-off backup codes: %w", err)
		}
		hashes := make([]string, 0, len(rows))
		for _, row := range rows {
			hashes = append(hashes, row.CodeHash)
		}

		if err := check(totp.DisableState{Role: account.Role(owner.Role), Factor: factor, BackupHashes: hashes}); err != nil {
			return err
		}

		// Cascading deletion removes every backup code with its factor.
		if err := queries.DeleteUserTOTP(ctx, userID); err != nil {
			return fmt.Errorf("remove TOTP factor: %w", err)
		}
		if err := queries.DeleteUserTOTPAttempt(ctx, sqlc.DeleteUserTOTPAttemptParams{UserID: userID, ID: attemptID}); err != nil {
			return fmt.Errorf("clear TOTP turn-off attempt: %w", err)
		}
		return nil
	})
}
