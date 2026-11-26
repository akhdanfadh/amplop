package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"amplop/internal/connectedaccount"
	"amplop/internal/repository/postgres/sqlc"
)

// InsertConnectState locks userID, calls create with when each of its saved
// states was created, and saves the state create returns, in one transaction;
// an AccountID of 0 is saved as no account. It returns
// [connectedaccount.ErrDeactivated] when userID is deactivated or gone.
func (db *DB) InsertConnectState(ctx context.Context, userID int64, create func([]time.Time) (connectedaccount.ConnectState, error)) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Lock the user, so two starts count each other's states.
		deactivated, err := queries.LockConnectStateUser(ctx, userID)
		// A deactivation ended the session this start was checked with.
		if errors.Is(err, pgx.ErrNoRows) || deactivated {
			return connectedaccount.ErrDeactivated
		}
		if err != nil {
			return fmt.Errorf("lock connect state user: %w", err)
		}

		// 2. Let create count the user's states and decide.
		createdAt, err := queries.ListConnectStateTimes(ctx, userID)
		if err != nil {
			return fmt.Errorf("list connect state times: %w", err)
		}
		state, err := create(createdAt)
		if err != nil {
			return err
		}

		// 3. Save the state. The locked user owns it, whatever create put in it.
		params := sqlc.InsertConnectStateParams{
			StateHash:    state.StateHash[:],
			UserID:       userID,
			SessionID:    state.SessionID,
			CodeVerifier: state.CodeVerifier,
			CreatedAt:    state.CreatedAt,
			ExpiresAt:    state.ExpiresAt,
		}
		if state.AccountID != 0 {
			params.AccountID = &state.AccountID
		}
		if err := queries.InsertConnectState(ctx, params); err != nil {
			return fmt.Errorf("insert connect state: %w", err)
		}
		return nil
	})
}

// DeleteExpiredConnectStates deletes up to limit states of any user expiring
// at or before expiredBy, skipping states another transaction holds, and
// returns how many it deleted.
func (db *DB) DeleteExpiredConnectStates(ctx context.Context, expiredBy time.Time, limit int) (int, error) {
	deleted, err := sqlc.New(db.pool).DeleteExpiredConnectStateBatch(ctx, sqlc.DeleteExpiredConnectStateBatchParams{
		ExpiredBy: expiredBy, BatchSize: int32(min(limit, math.MaxInt32)),
	})
	if err != nil {
		return 0, fmt.Errorf("delete expired connect states: %w", err)
	}
	return int(deleted), nil
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
		CreatedAt:    row.CreatedAt.UTC(),
		ExpiresAt:    row.ExpiresAt.UTC(),
	}
	if row.AccountID != nil {
		state.AccountID = *row.AccountID
	}
	return state, nil
}
