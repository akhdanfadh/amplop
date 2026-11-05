package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"inbok/internal/repository/postgres/sqlc"
	"inbok/internal/session"
)

// ConfirmTOTP activates userID's factor, replaces their sessions and backup
// codes, and clears the successful attempt, in one transaction, with the
// change update returns for the session with hash. It returns
// [session.ErrNotFound] when the session is gone or the owner is deactivated.
// An error rolls back every write.
func (db *DB) ConfirmTOTP(ctx context.Context, userID int64, hash [sha256.Size]byte, update func(session.EnrollmentState) (session.EnrollmentChange, error)) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Lock the owner, in the lock order enrollment restart and attempt
		// reservation use.
		deactivated, err := queries.LockTOTPUser(ctx, userID)
		// A deactivation ended the session this request was checked with.
		if errors.Is(err, pgx.ErrNoRows) || deactivated {
			return session.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock confirmation owner: %w", err)
		}

		// 2. Lock the session and read the factor.
		row, err := queries.LockUserSession(ctx, sqlc.LockUserSessionParams{UserID: userID, TokenHash: hash[:]})
		if errors.Is(err, pgx.ErrNoRows) {
			return session.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock confirmation session: %w", err)
		}
		factor, err := readTOTP(ctx, queries, userID)
		if err != nil {
			return err
		}

		// 3. Let update accept or refuse.
		state, err := sessionState(row)
		if err != nil {
			return err
		}
		next, err := update(session.EnrollmentState{Session: state, Factor: factor})
		if err != nil {
			return err
		}

		// 4. Activate the factor.
		err = queries.UpsertUserTOTP(ctx, sqlc.UpsertUserTOTPParams{
			UserID: userID, KeyID: next.Factor.KeyID, SecretCiphertext: next.Factor.Ciphertext,
			ConfirmedAt: next.Factor.ConfirmedAt, LastStep: next.Factor.LastStep,
		})
		if err != nil {
			return fmt.Errorf("confirm TOTP factor: %w", err)
		}

		// 5. Replace the whole set of backup codes, so no older hash survives
		// confirmation.
		if err := queries.DeleteTOTPBackupCodes(ctx, userID); err != nil {
			return fmt.Errorf("replace confirmation backup codes: %w", err)
		}
		for _, hash := range next.BackupHashes {
			err := queries.InsertTOTPBackupCode(ctx, sqlc.InsertTOTPBackupCodeParams{UserID: userID, CodeHash: hash})
			if err != nil {
				return fmt.Errorf("save confirmation backup code: %w", err)
			}
		}

		// 6. Remove every old password-only session before inserting its
		// replacement.
		if err := queries.DeleteUserSessions(ctx, userID); err != nil {
			return fmt.Errorf("end confirmation sessions: %w", err)
		}
		err = queries.InsertSession(ctx, insertSessionParams(next.Session))
		if err != nil {
			return fmt.Errorf("save confirmation session: %w", err)
		}

		// 7. Clear the attempt here, so it rolls back with confirmation and
		// nothing fails after the secrets are issued.
		err = queries.DeleteUserTOTPAttempt(ctx, sqlc.DeleteUserTOTPAttemptParams{UserID: userID, ID: next.AttemptID})
		if err != nil {
			return fmt.Errorf("clear confirmation attempt: %w", err)
		}
		return nil
	})
}
