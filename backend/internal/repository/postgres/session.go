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

// UpdateSession applies the change update returns for the session with hash,
// in one transaction. It returns [session.ErrNotFound] when no session has
// hash.
func (db *DB) UpdateSession(ctx context.Context, hash [sha256.Size]byte, update func(session.State) (session.Change, error)) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Lock the session, held through the decision and its commit, so
		// a waiting request sees refreshed activity or a deleted row.
		row, err := queries.LockSession(ctx, hash[:])
		if errors.Is(err, pgx.ErrNoRows) {
			return session.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock session: %w", err)
		}

		// 2. Let update decide, and apply its change.
		return applySessionChange(ctx, queries, row, update)
	})
}

// UpdateUserSession applies the change update returns for userID's session
// with public ID id, in one transaction, like [DB.UpdateSession]. It returns
// [session.ErrNotFound] when userID has no such session.
func (db *DB) UpdateUserSession(ctx context.Context, userID int64, id uuid.UUID, update func(session.State) (session.Change, error)) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Lock the session, held through the decision and its commit.
		row, err := queries.LockUserSessionByID(ctx, sqlc.LockUserSessionByIDParams{UserID: userID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return session.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock user session: %w", err)
		}

		// 2. Let update decide, and apply its change.
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
	return sessionStates(rows)
}

// DeleteUserOtherSessions deletes userID's sessions other than keep, in one
// transaction, if check accepts the kept session, and returns the deleted
// ones. It returns [session.ErrNotFound] when userID or keep is missing.
func (db *DB) DeleteUserOtherSessions(ctx context.Context, userID int64, keep [sha256.Size]byte, check func(session.State) error) ([]session.State, error) {
	var deleted []session.State
	err := pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Lock the owner. Taking it before the kept session's lock, as
		// [DB.FinishTOTP] does, keeps the two from deadlocking.
		if _, err := queries.LockSessionUser(ctx, userID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return session.ErrNotFound
			}
			return fmt.Errorf("lock session owner: %w", err)
		}

		// 2. Lock the kept session.
		row, err := queries.LockUserSession(ctx, sqlc.LockUserSessionParams{UserID: userID, TokenHash: keep[:]})
		if errors.Is(err, pgx.ErrNoRows) {
			return session.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock kept session: %w", err)
		}
		state, err := sessionState(row)
		if err != nil {
			return err
		}

		// 3. Let check accept or refuse the kept session.
		if err := check(state); err != nil {
			return err
		}

		// 4. Delete the other sessions in one statement.
		rows, err := queries.DeleteUserOtherSessions(ctx, sqlc.DeleteUserOtherSessionsParams{UserID: userID, Keep: keep[:]})
		if err != nil {
			return fmt.Errorf("delete other sessions: %w", err)
		}
		deleted, err = sessionStates(rows)
		return err
	})
	if err != nil {
		return nil, err
	}
	return deleted, nil
}

// deleteUserExpiredSessions runs in a sign-in transaction that holds the user's lock.
// Its error fails the sign-in: after a failed statement PostgreSQL aborts the
// transaction, so the session insert could not succeed anyway.
func deleteUserExpiredSessions(ctx context.Context, queries *sqlc.Queries, userID int64, expiry session.Expiry) error {
	err := queries.DeleteUserExpiredSessions(ctx, sqlc.DeleteUserExpiredSessionsParams{
		UserID: userID, ExpiresBy: expiry.ExpiresBy, LastUsedBy: expiry.LastUsedBy,
	})
	if err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	return nil
}

func sessionStates(rows []sqlc.Session) ([]session.State, error) {
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
		EmailVerified: row.EmailVerified,
	}, nil
}
