package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"amplop/internal/repository/postgres/sqlc"
	"amplop/internal/session"
)

// FinishTOTP activates userID's factor, makes its waiting codes the active
// backup codes, and replaces their sessions, in one transaction, with the
// change update returns for the session with hash. It returns
// [session.ErrNotFound] when the session is gone or the owner is deactivated.
// An error rolls back every write.
func (db *DB) FinishTOTP(ctx context.Context, userID int64, hash [sha256.Size]byte, update func(session.EnrollmentState) (session.EnrollmentChange, error)) error {
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
			return fmt.Errorf("lock finishing owner: %w", err)
		}

		// 2. Lock the session and read the factor and waiting set.
		row, err := queries.LockUserSession(ctx, sqlc.LockUserSessionParams{UserID: userID, TokenHash: hash[:]})
		if errors.Is(err, pgx.ErrNoRows) {
			return session.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock finishing session: %w", err)
		}
		factor, err := readTOTP(ctx, queries, userID)
		if err != nil {
			return err
		}
		waiting, err := readWaiting(ctx, queries, userID)
		if err != nil {
			return err
		}

		// 3. Let update accept or refuse.
		state, err := sessionState(row)
		if err != nil {
			return err
		}
		next, err := update(session.EnrollmentState{Session: state, Factor: factor, Waiting: waiting})
		if err != nil {
			return err
		}

		// 4. Activate the factor, make the waiting codes active, and delete
		// the waiting set with its display copy.
		if err := saveTOTP(ctx, queries, userID, next.Factor); err != nil {
			return fmt.Errorf("activate TOTP factor: %w", err)
		}
		if err := replaceBackupCodes(ctx, queries, userID, next.BackupHashes); err != nil {
			return err
		}
		if err := queries.DeleteTOTPWaitingBackupCodes(ctx, userID); err != nil {
			return fmt.Errorf("delete finished waiting codes: %w", err)
		}

		// 5. Remove every old session, including password-only ones, before
		// inserting the replacement.
		if err := queries.DeleteUserSessions(ctx, userID); err != nil {
			return fmt.Errorf("end finishing sessions: %w", err)
		}
		if err := queries.InsertSession(ctx, insertSessionParams(next.Session)); err != nil {
			return fmt.Errorf("save finishing session: %w", err)
		}
		return nil
	})
}
