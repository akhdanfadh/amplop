package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"inbok/internal/account"
	"inbok/internal/registration"
	"inbok/internal/repository/postgres/sqlc"
)

const (
	registrationOpenedAction = "registration-opened"
	registrationClosedAction = "registration-closed"
	memberRegisteredAction   = "member-registered"
)

// errNoLink means storage was asked to complete a registration or a
// password reset without a link.
var errNoLink = errors.New("link not found")

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
// and calls create. If create succeeds, it records the attempt and saves
// the link, in one transaction.
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

// ReadCompletion returns the registration state, the link with hash, and
// whether an account has the link's email, without a lock.
func (db *DB) ReadCompletion(ctx context.Context, hash [sha256.Size]byte) (registration.CompleteState, error) {
	record, err := db.ReadRegistration(ctx)
	if err != nil {
		return registration.CompleteState{}, err
	}

	queries := sqlc.New(db.pool)
	state := registration.CompleteState{Registration: record}
	row, err := queries.ReadRegistrationLink(ctx, hash[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return registration.CompleteState{}, fmt.Errorf("read registration link: %w", err)
	}
	state.Link = &registration.Link{TokenHash: hash, Email: row.Email, CreatedAt: row.CreatedAt.UTC(), ExpiresAt: row.ExpiresAt.UTC()}
	state.AccountExists, err = queries.HasUserEmail(ctx, row.Email)
	if err != nil {
		return registration.CompleteState{}, fmt.Errorf("read user email: %w", err)
	}
	return state, nil
}

// CompleteRegistration creates a member from the link with hash, in one
// transaction, if create accepts what it reads for the link and email, and
// returns the member.
func (db *DB) CompleteRegistration(ctx context.Context, hash [sha256.Size]byte, email string, create func(registration.CompleteState) (registration.CompleteChange, error)) (account.User, error) {
	var user account.User
	err := pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		// 1. Lock registration. Completions and closes wait for each other
		// here, so each sees the other's result.
		open, err := queries.LockRegistration(ctx)
		if err != nil {
			return fmt.Errorf("lock registration: %w", err)
		}
		// 2. Lock the system email settings. No saved settings have nothing to
		// lock and have not passed a test.
		tested, err := queries.LockSystemEmailTested(ctx)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("lock system email: %w", err)
		}
		// 3. Lock user writes. Setup inserts users too: waiting for it lets
		// the account check see its administrator.
		if err := queries.LockUsers(ctx); err != nil {
			return fmt.Errorf("lock users: %w", err)
		}

		// 4. Read the email's account and the link.
		state := registration.CompleteState{Registration: registration.Record{State: registrationState(open), SystemEmailTested: tested}}
		state.AccountExists, err = queries.HasUserEmail(ctx, email)
		if err != nil {
			return fmt.Errorf("read user email: %w", err)
		}
		row, err := queries.LockRegistrationLink(ctx, hash[:])
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("lock registration link: %w", err)
		}
		if err == nil {
			state.Link = &registration.Link{TokenHash: hash, Email: row.Email, CreatedAt: row.CreatedAt.UTC(), ExpiresAt: row.ExpiresAt.UTC()}
		}
		// 5. Let create accept or refuse.
		change, err := create(state)
		if err != nil {
			return err
		}
		// create must refuse a missing link; this guards other callers.
		if state.Link == nil {
			return errNoLink
		}

		// 6. Insert the member. The link decides the email, whatever the
		// caller passed.
		inserted, err := queries.InsertMemberUser(ctx, sqlc.InsertMemberUserParams{
			Email: state.Link.Email, PasswordHash: change.PasswordHash, EmailVerifiedAt: &change.VerifiedAt,
		})
		if err != nil {
			return fmt.Errorf("insert member user: %w", err)
		}
		// 7. Delete the email's links: the others would only answer that the
		// account exists.
		if err := queries.DeleteRegistrationLinksByEmail(ctx, inserted.Email); err != nil {
			return fmt.Errorf("delete registration links: %w", err)
		}
		// 8. Record the event. No session is needed to complete: whoever
		// holds the link acts, so the event names no actor.
		if err := queries.InsertAuditEvent(ctx, sqlc.InsertAuditEventParams{
			Action: memberRegisteredAction, TargetUserID: &inserted.ID,
		}); err != nil {
			return fmt.Errorf("record member registration: %w", err)
		}
		user = account.User{ID: inserted.ID, Email: inserted.Email, Role: account.Role(inserted.Role)}
		return nil
	})
	if err != nil {
		return account.User{}, err
	}
	return user, nil
}

// DeleteExpiredRegistrationLinks deletes up to limit links expiring at or
// before expiredBy, skipping links another transaction holds, and returns
// how many it deleted.
func (db *DB) DeleteExpiredRegistrationLinks(ctx context.Context, expiredBy time.Time, limit int) (int, error) {
	deleted, err := sqlc.New(db.pool).DeleteExpiredRegistrationLinkBatch(ctx, sqlc.DeleteExpiredRegistrationLinkBatchParams{
		ExpiredBy: expiredBy, BatchSize: int32(min(limit, math.MaxInt32)),
	})
	if err != nil {
		return 0, fmt.Errorf("delete expired registration links: %w", err)
	}
	return int(deleted), nil
}
