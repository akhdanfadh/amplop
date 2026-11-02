package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

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
// transaction. A system email save or test waits for the commit. Closing also
// deletes every registration attempt and link.
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
		if open {
			return nil
		}

		// The lock waits for requests in progress, so none saves a link after
		// the delete. Requests never lock the state, so this cannot deadlock.
		if err := queries.LockRegistrationAttempts(ctx); err != nil {
			return fmt.Errorf("lock registration attempts: %w", err)
		}
		if err := queries.DeleteAllRegistrationAttempts(ctx); err != nil {
			return fmt.Errorf("delete registration attempts: %w", err)
		}
		if err := queries.DeleteAllRegistrationLinks(ctx); err != nil {
			return fmt.Errorf("delete registration links: %w", err)
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

// RequestRegistration takes the lock every registration request shares,
// deletes attempts created at or before since, reads the state for email,
// and calls create. If create succeeds, it deletes expired registration and
// email verification links, records the attempt, and saves the link, in one
// transaction.
func (db *DB) RequestRegistration(ctx context.Context, email string, since time.Time, create func(registration.RequestState) (registration.RequestChange, error)) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		if err := queries.LockRegistrationAttempts(ctx); err != nil {
			return fmt.Errorf("lock registration attempts: %w", err)
		}
		if err := queries.DeleteOldRegistrationAttempts(ctx, since); err != nil {
			return fmt.Errorf("delete old registration attempts: %w", err)
		}
		// Not locked: a request only sends mail, and completing registration
		// checks the state again under its own lock.
		row, err := queries.ReadRegistration(ctx)
		if err != nil {
			return fmt.Errorf("read registration: %w", err)
		}
		exists, err := queries.HasUserEmail(ctx, email)
		if err != nil {
			return fmt.Errorf("read user email: %w", err)
		}
		rows, err := queries.ListRegistrationAttempts(ctx)
		if err != nil {
			return fmt.Errorf("read registration attempts: %w", err)
		}
		var attempts []registration.Attempt
		for _, row := range rows {
			attempts = append(attempts, registration.Attempt{
				Email:      row.Email,
				ClientAddr: row.ClientAddr,
				Outcome:    registration.Outcome(row.Outcome),
				CreatedAt:  row.CreatedAt.UTC(),
			})
		}

		change, err := create(registration.RequestState{
			Registration:  registration.Record{State: registrationState(row.Open), SystemEmailTested: row.SystemEmailTested},
			AccountExists: exists,
			Attempts:      attempts,
		})
		if err != nil {
			return err
		}

		if err := queries.DeleteExpiredRegistrationLinks(ctx, change.ExpiredBy); err != nil {
			return fmt.Errorf("delete expired registration links: %w", err)
		}
		if err := queries.DeleteAllExpiredEmailVerificationLinks(ctx, change.ExpiredBy); err != nil {
			return fmt.Errorf("delete expired email verification links: %w", err)
		}
		attempt := change.Attempt
		if err := queries.InsertRegistrationAttempt(ctx, sqlc.InsertRegistrationAttemptParams{
			Email: attempt.Email, ClientAddr: attempt.ClientAddr, Outcome: string(attempt.Outcome), CreatedAt: attempt.CreatedAt,
		}); err != nil {
			return fmt.Errorf("insert registration attempt: %w", err)
		}
		if change.Link == nil {
			return nil
		}
		link := change.Link
		if err := queries.InsertRegistrationLink(ctx, sqlc.InsertRegistrationLinkParams{
			TokenHash: link.TokenHash[:], Email: link.Email, CreatedAt: link.CreatedAt, ExpiresAt: link.ExpiresAt,
		}); err != nil {
			return fmt.Errorf("insert registration link: %w", err)
		}
		return nil
	})
}
