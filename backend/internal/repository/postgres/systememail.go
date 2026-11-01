package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

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
		Server: systememail.Server{
			Host:     row.Host,
			Port:     int(row.Port),
			Security: systememail.Security(row.Security),
		},
		FromAddress: row.FromAddress,
		FromName:    row.FromName,
		TestedAt:    row.TestedAt,
		Revision:    row.Revision,
	}
	if row.Username != nil {
		record.Username = *row.Username
	}
	if row.PasswordKeyID != nil {
		record.Password = &systememail.Password{KeyID: *row.PasswordKeyID, Ciphertext: row.PasswordCiphertext}
	}
	return record, nil
}

const systemEmailSavedAction = "system-email-saved"

// UpdateSystemEmail locks the settings, loads them, calls update, saves its
// result, and records a system-email-saved event for actorID, in one
// transaction.
func (db *DB) UpdateSystemEmail(ctx context.Context, actorID int64, update func(*systememail.Record) (systememail.Record, error)) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		// Lock the table, not the row: the keep-password decision reads the stored
		// row, so saves must not interleave, and the first save has no row to lock.
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
		// The event names only the actor: no account is affected, and setting
		// values are not recorded.
		if err := queries.InsertAuditEvent(ctx, sqlc.InsertAuditEventParams{Action: systemEmailSavedAction, ActorUserID: &actorID}); err != nil {
			return fmt.Errorf("record system email save: %w", err)
		}
		return nil
	})
}

// UpdateSystemEmailTestedAt saves testedAt as the tested time if the settings
// still have revision; otherwise it changes nothing.
func (db *DB) UpdateSystemEmailTestedAt(ctx context.Context, revision int64, testedAt *time.Time) error {
	err := sqlc.New(db.pool).UpdateSystemEmailTestedAt(ctx, sqlc.UpdateSystemEmailTestedAtParams{TestedAt: testedAt, Revision: revision})
	if err != nil {
		return fmt.Errorf("record system email test: %w", err)
	}
	return nil
}
