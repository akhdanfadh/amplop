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

// DisableTOTP removes userID's factor and backup codes and clears the attempt
// with attemptID, in one transaction, if check accepts the owner's role,
// factor, and backup codes. It returns [account.ErrUserNotFound] when the
// user is missing or deactivated.
func (db *DB) DisableTOTP(ctx context.Context, userID, attemptID int64, check func(totp.DisableState) error) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Lock the owner and read their current role under the lock.
		owner, err := queries.LockTOTPUserRole(ctx, userID)
		// A deactivation ended the session this request was checked with.
		if errors.Is(err, pgx.ErrNoRows) || owner.Deactivated {
			return account.ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("lock TOTP turn-off owner: %w", err)
		}

		// 2. Read the factor and the backup codes.
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

		// 3. Let check accept or refuse.
		if err := check(totp.DisableState{Role: account.Role(owner.Role), Factor: factor, BackupHashes: hashes}); err != nil {
			return err
		}

		// 4. Remove the factor. Cascading deletion removes every backup code
		// with it.
		if err := queries.DeleteUserTOTP(ctx, userID); err != nil {
			return fmt.Errorf("remove TOTP factor: %w", err)
		}

		// 5. Clear the attempt, which rolls back with the removal.
		if err := queries.DeleteUserTOTPAttempt(ctx, sqlc.DeleteUserTOTPAttemptParams{UserID: userID, ID: attemptID}); err != nil {
			return fmt.Errorf("clear TOTP turn-off attempt: %w", err)
		}
		return nil
	})
}
