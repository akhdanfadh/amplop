package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"inbok/internal/connectedaccount"
	"inbok/internal/repository/postgres/sqlc"
)

// InsertConnectState saves state; an AccountID of 0 is saved as no account.
func (db *DB) InsertConnectState(ctx context.Context, state connectedaccount.ConnectState) error {
	params := sqlc.InsertConnectStateParams{
		StateHash:    state.StateHash[:],
		UserID:       state.UserID,
		SessionID:    state.SessionID,
		CodeVerifier: state.CodeVerifier,
		ExpiresAt:    state.ExpiresAt,
	}
	if state.AccountID != 0 {
		params.AccountID = &state.AccountID
	}
	if err := sqlc.New(db.pool).InsertConnectState(ctx, params); err != nil {
		return fmt.Errorf("insert connect state: %w", err)
	}
	return nil
}

// DeleteConnectState deletes the state saved under stateHash and returns
// it, or [connectedaccount.ErrStateInvalid] when there is none.
func (db *DB) DeleteConnectState(ctx context.Context, stateHash [sha256.Size]byte) (connectedaccount.ConnectState, error) {
	row, err := sqlc.New(db.pool).DeleteConnectState(ctx, stateHash[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return connectedaccount.ConnectState{}, connectedaccount.ErrStateInvalid
	}
	if err != nil {
		return connectedaccount.ConnectState{}, fmt.Errorf("delete connect state: %w", err)
	}
	state := connectedaccount.ConnectState{
		StateHash:    stateHash,
		UserID:       row.UserID,
		SessionID:    row.SessionID,
		CodeVerifier: row.CodeVerifier,
		ExpiresAt:    row.ExpiresAt.UTC(),
	}
	if row.AccountID != nil {
		state.AccountID = *row.AccountID
	}
	return state, nil
}
