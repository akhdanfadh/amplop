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
	return &totp.Factor{KeyID: row.KeyID, Ciphertext: row.SecretCiphertext, ConfirmedAt: row.ConfirmedAt, LastStep: row.LastStep}, nil
}

// UpdateTOTP locks the owner before reading, so first enrollment and restart
// serialize even when there is no factor row to lock yet.
func (db *DB) UpdateTOTP(ctx context.Context, userID int64, update func(*totp.Factor) (totp.Factor, error)) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		if _, err := queries.LockTOTPUser(ctx, userID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return account.ErrUserNotFound
			}
			return fmt.Errorf("lock TOTP owner: %w", err)
		}
		current, err := readTOTP(ctx, queries, userID)
		if err != nil {
			return err
		}
		next, err := update(current)
		if err != nil {
			return err
		}
		err = queries.UpsertUserTOTP(ctx, sqlc.UpsertUserTOTPParams{
			UserID: userID, KeyID: next.KeyID, SecretCiphertext: next.Ciphertext,
			ConfirmedAt: next.ConfirmedAt, LastStep: next.LastStep,
		})
		if err != nil {
			return fmt.Errorf("save TOTP: %w", err)
		}
		return nil
	})
}
