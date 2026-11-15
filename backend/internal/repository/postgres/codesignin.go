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

// ChallengeUser locates the limiter owner without granting session access.
func (db *DB) ChallengeUser(ctx context.Context, hash [sha256.Size]byte) (int64, error) {
	id, err := sqlc.New(db.pool).ReadSignInChallengeUser(ctx, hash[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, session.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("read challenge owner: %w", err)
	}
	return id, nil
}

// CompleteSignIn saves the outcome update returns for userID's challenge with
// hash, in one transaction: a counted failure, a deleted challenge, or a new
// session that consumes the challenge and the code used. It returns
// [session.ErrNotFound] when the user or the challenge is missing.
func (db *DB) CompleteSignIn(ctx context.Context, userID int64, hash [sha256.Size]byte, update func(session.CodeSignInState) (session.CodeSignInChange, error)) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Lock the owner, the lock every TOTP write takes first.
		deactivated, err := queries.LockTOTPUser(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return session.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock code sign-in owner: %w", err)
		}

		// 2. Lock the challenge.
		row, err := queries.LockSignInChallenge(ctx, sqlc.LockSignInChallengeParams{UserID: userID, TokenHash: hash[:]})
		if errors.Is(err, pgx.ErrNoRows) {
			return session.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock sign-in challenge: %w", err)
		}

		// 3. Read the factor and the backup codes.
		factor, err := readTOTP(ctx, queries, userID)
		if err != nil {
			return err
		}
		rows, err := queries.ReadTOTPBackupCodes(ctx, userID)
		if err != nil {
			return fmt.Errorf("read sign-in backup codes: %w", err)
		}
		codes := make([]session.BackupCode, 0, len(rows))
		for _, row := range rows {
			codes = append(codes, session.BackupCode{ID: row.ID, Hash: row.CodeHash})
		}

		// 4. Let update decide.
		next, err := update(session.CodeSignInState{
			Challenge: session.Challenge{TokenHash: hash, UserID: row.UserID, ExpiresAt: row.ExpiresAt.UTC(), Failures: int(row.Failures)},
			Factor:    factor, BackupCodes: codes, Deactivated: deactivated,
		})
		if err != nil {
			return err
		}

		// 5. Count a failure and keep the challenge for another try.
		if next.Action == session.CodeActionFail {
			if err := queries.UpdateSignInChallengeFailures(ctx, sqlc.UpdateSignInChallengeFailuresParams{
				UserID: userID, TokenHash: hash[:], Failures: int32(next.Failures),
			}); err != nil {
				return fmt.Errorf("count challenge failure: %w", err)
			}
			return nil
		}
		if next.Action != session.CodeActionDelete && next.Action != session.CodeActionComplete {
			return errors.New("invalid code sign-in action")
		}

		// 6. Otherwise consume the challenge. A refused one ends here.
		if _, err := queries.DeleteSignInChallenge(ctx, sqlc.DeleteSignInChallengeParams{UserID: userID, TokenHash: hash[:]}); err != nil {
			return fmt.Errorf("consume sign-in challenge: %w", err)
		}
		if next.Action == session.CodeActionDelete {
			return nil
		}

		// 7. Consume the TOTP step or the backup code used.
		if next.Factor != nil {
			if err := saveTOTP(ctx, queries, userID, *next.Factor); err != nil {
				return fmt.Errorf("consume sign-in TOTP step: %w", err)
			}
		}
		if next.BackupCodeID != 0 {
			if _, err := queries.ConsumeTOTPBackupCode(ctx, sqlc.ConsumeTOTPBackupCodeParams{UserID: userID, ID: next.BackupCodeID}); err != nil {
				return fmt.Errorf("consume sign-in backup code: %w", err)
			}
		}

		// 8. Delete the user's expired sessions and save the new one.
		if err := deleteUserExpiredSessions(ctx, queries, userID, next.Expiry); err != nil {
			return err
		}
		if err := queries.InsertSession(ctx, insertSessionParams(next.Session)); err != nil {
			return fmt.Errorf("save code sign-in session: %w", err)
		}

		// 9. Clear the attempt, which rolls back with the sign-in.
		if err := queries.DeleteUserTOTPAttempt(ctx, sqlc.DeleteUserTOTPAttemptParams{UserID: userID, ID: next.AttemptID}); err != nil {
			return fmt.Errorf("clear code sign-in attempt: %w", err)
		}
		return nil
	})
}
