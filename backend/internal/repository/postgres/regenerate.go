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

// ReplaceTOTPBackupCodes saves the factor and the backup codes update returns
// for userID's current factor and clears its attempt, in one transaction, so
// the old set stays usable unless the whole change commits. It returns
// [account.ErrUserNotFound] when the user is missing or deactivated.
func (db *DB) ReplaceTOTPBackupCodes(ctx context.Context, userID int64, update func(*totp.Factor) (totp.BackupChange, error)) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Lock the owner.
		deactivated, err := queries.LockTOTPUser(ctx, userID)
		// A deactivation ended the session this request was checked with.
		if errors.Is(err, pgx.ErrNoRows) || deactivated {
			return account.ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("lock backup-code owner: %w", err)
		}

		// 2. Read the factor and let update decide.
		current, err := readTOTP(ctx, queries, userID)
		if err != nil {
			return err
		}
		next, err := update(current)
		if err != nil {
			return err
		}

		// 3. Save the factor, which consumes the code's step.
		err = queries.UpsertUserTOTP(ctx, sqlc.UpsertUserTOTPParams{
			UserID: userID, KeyID: next.Factor.KeyID, SecretCiphertext: next.Factor.Ciphertext,
			ConfirmedAt: next.Factor.ConfirmedAt, LastStep: next.Factor.LastStep,
		})
		if err != nil {
			return fmt.Errorf("consume backup-code TOTP step: %w", err)
		}

		// 4. Replace the whole set of backup codes.
		if err := queries.DeleteTOTPBackupCodes(ctx, userID); err != nil {
			return fmt.Errorf("remove old backup codes: %w", err)
		}
		for _, hash := range next.Hashes {
			if err := queries.InsertTOTPBackupCode(ctx, sqlc.InsertTOTPBackupCodeParams{UserID: userID, CodeHash: hash}); err != nil {
				return fmt.Errorf("save replacement backup code: %w", err)
			}
		}

		// 5. Clear the attempt here, so success cannot fail later while the
		// new codes are revealed.
		if err := queries.DeleteUserTOTPAttempt(ctx, sqlc.DeleteUserTOTPAttemptParams{UserID: userID, ID: next.AttemptID}); err != nil {
			return fmt.Errorf("clear backup-code attempt: %w", err)
		}
		return nil
	})
}
