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

var _ session.Storage = (*DB)(nil)

// InsertSession saves the hash and fixed lifetime chosen by the session rules.
func (db *DB) InsertSession(ctx context.Context, state session.State) error {
	err := sqlc.New(db.pool).InsertSession(ctx, sqlc.InsertSessionParams{
		TokenHash: state.TokenHash[:], UserID: state.UserID,
		CreatedAt: state.CreatedAt, LastUsedAt: state.LastUsedAt, ExpiresAt: state.ExpiresAt,
	})
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

// UpdateSession keeps the row locked through the decision and its commit.
// Waiting requests then see refreshed activity or a deleted row.
func (db *DB) UpdateSession(ctx context.Context, hash [sha256.Size]byte, update func(session.State) (session.Change, error)) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		row, err := queries.LockSession(ctx, hash[:])
		if errors.Is(err, pgx.ErrNoRows) {
			return session.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock session: %w", err)
		}

		change, err := update(session.State{
			TokenHash: hash, UserID: row.UserID, CreatedAt: row.CreatedAt.UTC(),
			LastUsedAt: row.LastUsedAt.UTC(), ExpiresAt: row.ExpiresAt.UTC(),
		})
		if err != nil {
			return err
		}

		switch change.Action {
		case session.Keep:
			return nil
		case session.Touch:
			if err := queries.TouchSession(ctx, sqlc.TouchSessionParams{TokenHash: hash[:], LastUsedAt: change.LastUsedAt}); err != nil {
				return fmt.Errorf("touch session: %w", err)
			}
			return nil
		case session.Delete:
			if err := queries.DeleteSession(ctx, hash[:]); err != nil {
				return fmt.Errorf("delete session: %w", err)
			}
			return nil
		default:
			return errors.New("invalid session action")
		}
	})
}

// DeleteSession ends a session; deleting an absent row is harmless.
func (db *DB) DeleteSession(ctx context.Context, hash [sha256.Size]byte) error {
	if err := sqlc.New(db.pool).DeleteSession(ctx, hash[:]); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
