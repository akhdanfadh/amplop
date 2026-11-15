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

// ReadTOTPStatus reads only the metadata needed for display, nil if absent.
func (db *DB) ReadTOTPStatus(ctx context.Context, userID int64) (*totp.StatusRecord, error) {
	row, err := sqlc.New(db.pool).ReadUserTOTPStatus(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read TOTP status: %w", err)
	}

	return &totp.StatusRecord{
		ConfirmedAt: row.ConfirmedAt, StartedAt: row.StartedAt,
		BackupCodesLeft: int(row.BackupCodesLeft), WaitingExpiresAt: row.WaitingExpiresAt,
	}, nil
}

// ReadTOTP returns nil when the user has no factor.
func (db *DB) ReadTOTP(ctx context.Context, userID int64) (*totp.Factor, error) {
	return readTOTP(ctx, sqlc.New(db.pool), userID)
}

func readTOTP(ctx context.Context, queries *sqlc.Queries, userID int64) (*totp.Factor, error) {
	row, err := queries.ReadUserTOTP(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read TOTP: %w", err)
	}
	return &totp.Factor{
		KeyID: row.KeyID, Ciphertext: row.SecretCiphertext, ConfirmedAt: row.ConfirmedAt,
		LastStep: row.LastStep, StartedAt: row.StartedAt,
	}, nil
}

// saveTOTP saves userID's factor.
func saveTOTP(ctx context.Context, queries *sqlc.Queries, userID int64, factor totp.Factor) error {
	return queries.UpsertUserTOTP(ctx, sqlc.UpsertUserTOTPParams{
		UserID: userID, KeyID: factor.KeyID, SecretCiphertext: factor.Ciphertext,
		ConfirmedAt: factor.ConfirmedAt, LastStep: factor.LastStep, StartedAt: factor.StartedAt,
	})
}

// readWaiting returns userID's waiting backup-code set, nil when none. A set
// without a display copy has an empty key and ciphertext.
func readWaiting(ctx context.Context, queries *sqlc.Queries, userID int64) (*totp.Waiting, error) {
	row, err := queries.ReadTOTPWaitingBackupCodes(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read waiting backup codes: %w", err)
	}
	waiting := &totp.Waiting{Hashes: row.CodeHashes, Ciphertext: row.CodesCiphertext, ExpiresAt: row.ExpiresAt}
	if row.KeyID != nil {
		waiting.KeyID = *row.KeyID
	}
	if copy(waiting.TokenHash[:], row.TokenHash) != len(waiting.TokenHash) {
		return nil, errors.New("invalid waiting backup-code token hash")
	}
	return waiting, nil
}

// replaceBackupCodes replaces userID's whole set of active backup codes, so
// no older hash survives.
func replaceBackupCodes(ctx context.Context, queries *sqlc.Queries, userID int64, hashes []string) error {
	if err := queries.DeleteTOTPBackupCodes(ctx, userID); err != nil {
		return fmt.Errorf("remove old backup codes: %w", err)
	}
	for _, hash := range hashes {
		if err := queries.InsertTOTPBackupCode(ctx, sqlc.InsertTOTPBackupCodeParams{UserID: userID, CodeHash: hash}); err != nil {
			return fmt.Errorf("save backup code: %w", err)
		}
	}
	return nil
}

// writeWaiting keeps, deletes, or saves userID's waiting set as change says.
// A set without a display copy saves NULL for its key and ciphertext.
func writeWaiting(ctx context.Context, queries *sqlc.Queries, userID int64, change totp.Change) error {
	switch change.WaitingWrite {
	case totp.KeepWaiting:
		return nil
	case totp.DeleteWaiting:
		if err := queries.DeleteTOTPWaitingBackupCodes(ctx, userID); err != nil {
			return fmt.Errorf("delete waiting backup codes: %w", err)
		}
		return nil
	case totp.SaveWaiting:
		w := change.Waiting
		if w == nil {
			return errors.New("save waiting backup codes: no set")
		}
		params := sqlc.UpsertTOTPWaitingBackupCodesParams{
			UserID: userID, CodeHashes: w.Hashes, TokenHash: w.TokenHash[:], ExpiresAt: w.ExpiresAt,
		}
		if w.KeyID != "" || len(w.Ciphertext) > 0 {
			params.KeyID, params.CodesCiphertext = &w.KeyID, w.Ciphertext
		}
		if err := queries.UpsertTOTPWaitingBackupCodes(ctx, params); err != nil {
			return fmt.Errorf("save waiting backup codes: %w", err)
		}
		return nil
	}
	return fmt.Errorf("unknown waiting write %d", change.WaitingWrite)
}

// UpdateTOTP saves the [totp.Change] update returns for userID's factor and
// waiting set, each nil when absent, in one transaction. It returns
// [account.ErrUserNotFound] when the user is missing or deactivated.
func (db *DB) UpdateTOTP(ctx context.Context, userID int64, update func(totp.Record) (totp.Change, error)) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Lock the owner before reading, so first enrollment and restart
		// serialize even when there is no factor row to lock yet.
		deactivated, err := queries.LockTOTPUser(ctx, userID)
		// A deactivation ended the session this request was checked with.
		if errors.Is(err, pgx.ErrNoRows) || deactivated {
			return account.ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("lock TOTP owner: %w", err)
		}

		// 2. Read the factor and waiting set, and let update decide.
		factor, err := readTOTP(ctx, queries, userID)
		if err != nil {
			return err
		}
		waiting, err := readWaiting(ctx, queries, userID)
		if err != nil {
			return err
		}
		next, err := update(totp.Record{Factor: factor, Waiting: waiting})
		if err != nil {
			return err
		}

		// 3. Save the factor first: the waiting set references it.
		if next.Factor != nil {
			if err := saveTOTP(ctx, queries, userID, *next.Factor); err != nil {
				return fmt.Errorf("save TOTP: %w", err)
			}
		}
		if err := writeWaiting(ctx, queries, userID, next); err != nil {
			return err
		}
		if next.BackupHashes != nil {
			if err := replaceBackupCodes(ctx, queries, userID, next.BackupHashes); err != nil {
				return err
			}
		}

		// 4. Clear the attempt here, so it rolls back with the change and
		// nothing fails after codes are revealed.
		if next.AttemptID != 0 {
			err := queries.DeleteUserTOTPAttempt(ctx, sqlc.DeleteUserTOTPAttemptParams{UserID: userID, ID: next.AttemptID})
			if err != nil {
				return fmt.Errorf("clear TOTP attempt: %w", err)
			}
		}
		return nil
	})
}

// CancelTOTP deletes userID's factor, in one transaction, if check accepts it,
// nil when none. It returns [account.ErrUserNotFound] when the user is
// missing or deactivated.
func (db *DB) CancelTOTP(ctx context.Context, userID int64, check func(*totp.Factor) error) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Lock the owner, the lock enrollment and confirmation share.
		deactivated, err := queries.LockTOTPUser(ctx, userID)
		// A deactivation ended the session this request was checked with.
		if errors.Is(err, pgx.ErrNoRows) || deactivated {
			return account.ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("lock TOTP cancellation owner: %w", err)
		}

		// 2. Read the factor and let check accept or refuse.
		current, err := readTOTP(ctx, queries, userID)
		if err != nil {
			return err
		}
		if err := check(current); err != nil {
			return err
		}

		// 3. Delete the factor. Cascading deletion removes its waiting set.
		if err := queries.DeleteUserTOTP(ctx, userID); err != nil {
			return fmt.Errorf("cancel TOTP enrollment: %w", err)
		}
		return nil
	})
}
