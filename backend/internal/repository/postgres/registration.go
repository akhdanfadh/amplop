package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"inbok/internal/registration"
	"inbok/internal/repository/postgres/sqlc"
)

const (
	registrationOpenedAction = "registration-opened"
	registrationClosedAction = "registration-closed"
)

// ReadRegistration returns the saved state and whether the system email
// settings in use passed a test.
func (db *DB) ReadRegistration(ctx context.Context) (registration.Record, error) {
	row, err := sqlc.New(db.pool).ReadRegistration(ctx)
	if err != nil {
		return registration.Record{}, fmt.Errorf("read registration: %w", err)
	}
	return registration.Record{State: registrationState(row.Open), SystemEmailTested: row.SystemEmailTested}, nil
}

// UpdateRegistration locks the state and the system email settings, loads
// them, calls update, saves the state update returns, and records a
// registration-opened or registration-closed event for actorID, in one
// transaction. A system email save or test waits for the commit.
func (db *DB) UpdateRegistration(ctx context.Context, actorID int64, update func(registration.Record) (registration.State, error)) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		open, err := queries.LockRegistration(ctx)
		if err != nil {
			return fmt.Errorf("lock registration: %w", err)
		}
		// No saved settings have nothing to lock and have not passed a test.
		tested, err := queries.LockSystemEmailTested(ctx)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("lock system email: %w", err)
		}
		state, err := update(registration.Record{State: registrationState(open), SystemEmailTested: tested})
		if err != nil {
			return err
		}

		open = state == registration.StateOpen
		if err := queries.UpdateRegistration(ctx, open); err != nil {
			return fmt.Errorf("save registration: %w", err)
		}
		// The event names only the actor: no account is affected.
		action := registrationClosedAction
		if open {
			action = registrationOpenedAction
		}
		if err := queries.InsertAuditEvent(ctx, sqlc.InsertAuditEventParams{Action: action, ActorUserID: &actorID}); err != nil {
			return fmt.Errorf("record registration save: %w", err)
		}
		return nil
	})
}

// registrationState returns the State the registration.open column stores.
func registrationState(open bool) registration.State {
	if open {
		return registration.StateOpen
	}
	return registration.StateClosed
}
