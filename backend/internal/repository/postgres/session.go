package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5"

	"inbok/internal/account"
	"inbok/internal/repository/postgres/sqlc"
	"inbok/internal/session"
)

// Compile-time check that *DB implements session.Storage.
var _ session.Storage = (*DB)(nil)

// InsertSession saves the hash and fixed lifetime chosen by the session rules.
func (db *DB) InsertSession(ctx context.Context, state session.State) error {
	if err := sqlc.New(db.pool).InsertSession(ctx, insertSessionParams(state)); err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

// insertSessionParams saves an empty User-Agent or invalid address as NULL.
// Storage assigns the public ID, so state.ID is ignored.
func insertSessionParams(state session.State) sqlc.InsertSessionParams {
	params := sqlc.InsertSessionParams{
		TokenHash: state.TokenHash[:], UserID: state.UserID,
		CreatedAt: state.CreatedAt, LastUsedAt: state.LastUsedAt, ExpiresAt: state.ExpiresAt,
	}
	if agent := state.Device.UserAgent; agent != "" {
		params.UserAgent = &agent
	}
	if addr := state.Device.Addr; addr.IsValid() {
		params.SignedInFrom = &addr
	}
	return params
}

// sessionState reads a stored row the way the session rules expect it.
func sessionState(row sqlc.Session) (session.State, error) {
	if len(row.TokenHash) != sha256.Size {
		return session.State{}, errors.New("invalid stored session hash")
	}
	state := session.State{
		TokenHash: [sha256.Size]byte(row.TokenHash), ID: row.ID, UserID: row.UserID,
		CreatedAt: row.CreatedAt.UTC(), LastUsedAt: row.LastUsedAt.UTC(), ExpiresAt: row.ExpiresAt.UTC(),
	}
	if row.UserAgent != nil {
		state.Device.UserAgent = *row.UserAgent
	}
	if row.SignedInFrom != nil {
		state.Device.Addr = *row.SignedInFrom
	}
	return state, nil
}

// UpdateSession applies the callback's session change in one transaction.
// It keeps the row locked through the decision and its commit.
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
		return applySessionChange(ctx, queries, row, update)
	})
}

// UpdateUserSession locks the session by owner and public ID, then applies
// the callback's change like UpdateSession.
func (db *DB) UpdateUserSession(ctx context.Context, userID int64, id uuid.UUID, update func(session.State) (session.Change, error)) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		row, err := queries.LockUserSessionByID(ctx, sqlc.LockUserSessionByIDParams{UserID: userID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return session.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock user session: %w", err)
		}
		return applySessionChange(ctx, queries, row, update)
	})
}

// applySessionChange runs inside the transaction holding the row's lock.
func applySessionChange(ctx context.Context, queries *sqlc.Queries, row sqlc.Session, update func(session.State) (session.Change, error)) error {
	state, err := sessionState(row)
	if err != nil {
		return err
	}
	change, err := update(state)
	if err != nil {
		return err
	}

	switch change.Action {
	case session.Keep:
		return nil
	case session.Touch:
		if err := queries.TouchSession(ctx, sqlc.TouchSessionParams{TokenHash: row.TokenHash, LastUsedAt: change.LastUsedAt}); err != nil {
			return fmt.Errorf("touch session: %w", err)
		}
		return nil
	case session.Delete:
		if err := queries.DeleteSession(ctx, row.TokenHash); err != nil {
			return fmt.Errorf("delete session: %w", err)
		}
		return nil
	default:
		return errors.New("invalid session action")
	}
}

// DeleteSession ends a session; deleting an absent row is harmless.
func (db *DB) DeleteSession(ctx context.Context, hash [sha256.Size]byte) error {
	if err := sqlc.New(db.pool).DeleteSession(ctx, hash[:]); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// ReadUserSessions returns the user's rows; the session rules decide which are live.
func (db *DB) ReadUserSessions(ctx context.Context, userID int64) ([]session.State, error) {
	rows, err := sqlc.New(db.pool).ReadUserSessions(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("read user sessions: %w", err)
	}
	states := make([]session.State, 0, len(rows))
	for _, row := range rows {
		state, err := sessionState(row)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}

// ReadSessionIdentity never derives access from a session that no longer exists.
func (db *DB) ReadSessionIdentity(ctx context.Context, hash [sha256.Size]byte) (session.IdentityState, error) {
	row, err := sqlc.New(db.pool).ReadSessionIdentity(ctx, hash[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return session.IdentityState{}, session.ErrNotFound
	}
	if err != nil {
		return session.IdentityState{}, fmt.Errorf("read session identity: %w", err)
	}

	return session.IdentityState{
		User:          account.User{ID: row.ID, Email: row.Email, Role: account.Role(row.Role)},
		TOTPConfirmed: row.TotpConfirmed,
	}, nil
}
