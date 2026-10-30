package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"inbok/internal/repository/postgres/sqlc"
	"inbok/internal/systememail"
)

// ReadSystemEmail returns nil when no settings are saved.
func (db *DB) ReadSystemEmail(ctx context.Context) (*systememail.Record, error) {
	return readSystemEmail(ctx, sqlc.New(db.pool))
}

func readSystemEmail(ctx context.Context, queries *sqlc.Queries) (*systememail.Record, error) {
	row, err := queries.ReadSystemEmail(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read system email: %w", err)
	}

	record := &systememail.Record{
		Host:        row.Host,
		Port:        int(row.Port),
		Security:    systememail.Security(row.Security),
		FromAddress: row.FromAddress,
		FromName:    row.FromName,
		TestedAt:    row.TestedAt,
	}
	if row.Username != nil {
		record.Username = *row.Username
	}
	if row.PasswordKeyID != nil {
		record.Password = &systememail.Password{KeyID: *row.PasswordKeyID, Ciphertext: row.PasswordCiphertext}
	}
	return record, nil
}

// UpdateSystemEmail locks the table rather than the row, so two first saves
// serialize even though neither has a row to lock yet.
func (db *DB) UpdateSystemEmail(ctx context.Context, update func(*systememail.Record) (systememail.Record, error)) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		if err := queries.LockSystemEmail(ctx); err != nil {
			return fmt.Errorf("lock system email: %w", err)
		}
		current, err := readSystemEmail(ctx, queries)
		if err != nil {
			return err
		}
		next, err := update(current)
		if err != nil {
			return err
		}

		params := sqlc.UpsertSystemEmailParams{
			Host:        next.Host,
			Port:        int32(next.Port), // systememail limits ports to 1 to 65535.
			Security:    string(next.Security),
			FromAddress: next.FromAddress,
			FromName:    next.FromName,
			TestedAt:    next.TestedAt,
		}
		if next.Username != "" {
			params.Username = &next.Username
		}
		if next.Password != nil {
			params.PasswordKeyID, params.PasswordCiphertext = &next.Password.KeyID, next.Password.Ciphertext
		}
		if err := queries.UpsertSystemEmail(ctx, params); err != nil {
			return fmt.Errorf("save system email: %w", err)
		}
		return nil
	})
}
