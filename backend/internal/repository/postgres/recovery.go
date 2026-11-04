package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"inbok/internal/account"
	"inbok/internal/repository/postgres/sqlc"
)

const adminRecoveredAction = "admin-recovered"

// UpdateRecovery replaces credentials and clears authentication data together.
// The callback sees the current account under the same lock as TOTP writes.
func (db *DB) UpdateRecovery(ctx context.Context, userID int64, update func(account.User) (string, error)) (account.RecoveryResult, error) {
	var result account.RecoveryResult
	err := pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		row, err := queries.LockRecoveryUser(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return account.ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("lock recovery owner: %w", err)
		}

		hash, err := update(account.User{ID: row.ID, Email: row.Email, Role: account.Role(row.Role)})
		if err != nil {
			return err
		}
		result.BackupCodes, err = queries.CountUserBackupCodes(ctx, userID)
		if err != nil {
			return fmt.Errorf("count recovery backup codes: %w", err)
		}

		if err := queries.UpdateUserPassword(ctx, sqlc.UpdateUserPasswordParams{UserID: userID, PasswordHash: hash}); err != nil {
			return fmt.Errorf("replace recovery password: %w", err)
		}
		// Removing the factor cascades to backup codes, without decrypting secrets.
		result.Factors, err = queries.DeleteRecoveryUserTOTP(ctx, userID)
		if err != nil {
			return fmt.Errorf("remove recovery factor: %w", err)
		}
		result.Sessions, err = queries.DeleteRecoverySessions(ctx, userID)
		if err != nil {
			return fmt.Errorf("end recovery sessions: %w", err)
		}
		result.Challenges, err = queries.DeleteUserSignInChallenges(ctx, userID)
		if err != nil {
			return fmt.Errorf("remove recovery challenges: %w", err)
		}
		result.TOTPAttempts, err = queries.DeleteRecoveryTOTPAttempts(ctx, userID)
		if err != nil {
			return fmt.Errorf("clear recovery TOTP attempts: %w", err)
		}
		// Saved emails and throttle keys share emailaddress.Parse's form.
		result.SignInAttempts, err = queries.DeleteRecoverySignInAttempts(ctx, &row.Email)
		if err != nil {
			return fmt.Errorf("clear recovery sign-in attempts: %w", err)
		}
		// A link sent before recovery could otherwise set a password the
		// operator did not choose, before the administrator enrolls TOTP.
		result.ResetLinks, err = queries.DeleteUserPasswordResetLinks(ctx, userID)
		if err != nil {
			return fmt.Errorf("delete recovery reset links: %w", err)
		}
		if err := queries.InsertAuditEvent(ctx, sqlc.InsertAuditEventParams{Action: adminRecoveredAction, TargetUserID: &userID}); err != nil {
			return fmt.Errorf("record administrator recovery: %w", err)
		}
		return nil
	})
	if err != nil {
		return account.RecoveryResult{}, err
	}
	return result, nil
}

// ReadRecoveryNotice implements the recovery notice's storage: the current
// email of the user with recipientID and whether they are active, and the
// email of the user with recoveredID, empty when that user is gone. A
// missing recipient returns [account.ErrUserNotFound].
func (db *DB) ReadRecoveryNotice(ctx context.Context, recoveredID, recipientID int64) (account.RecoveryNoticeState, error) {
	row, err := sqlc.New(db.pool).ReadRecoveryNotice(ctx, sqlc.ReadRecoveryNoticeParams{RecoveredID: recoveredID, RecipientID: recipientID})
	if errors.Is(err, pgx.ErrNoRows) {
		return account.RecoveryNoticeState{}, account.ErrUserNotFound
	}
	if err != nil {
		return account.RecoveryNoticeState{}, fmt.Errorf("read recovery notice: %w", err)
	}
	state := account.RecoveryNoticeState{RecipientEmail: row.RecipientEmail, RecipientActive: row.RecipientActive}
	if row.RecoveredEmail != nil {
		state.RecoveredEmail = *row.RecoveredEmail
	}
	return state, nil
}

// ListActiveAdministrators implements the recovery notice's storage: the
// IDs of the administrators that are not deactivated, oldest first.
func (db *DB) ListActiveAdministrators(ctx context.Context) ([]int64, error) {
	ids, err := sqlc.New(db.pool).ListActiveAdministrators(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active administrators: %w", err)
	}
	return ids, nil
}
