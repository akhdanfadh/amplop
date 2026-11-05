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
	"inbok/internal/passwordreset"
	"inbok/internal/repository/postgres/sqlc"
)

const passwordResetAction = "password-reset"

// errNoAccount means storage was asked to save a reset link for an email
// without an account.
var errNoAccount = errors.New("password reset link has no account")

// ReadPasswordReset returns the link with hash and its user, without a
// lock.
func (db *DB) ReadPasswordReset(ctx context.Context, hash [sha256.Size]byte) (passwordreset.CompleteState, error) {
	return readPasswordReset(ctx, sqlc.New(db.pool), hash)
}

// CompletePasswordReset replaces the password of the user of the link with
// hash, in one transaction under that user's lock, if complete accepts what
// it reads, and returns the user.
func (db *DB) CompletePasswordReset(ctx context.Context, hash [sha256.Size]byte, complete func(passwordreset.CompleteState) (passwordreset.CompleteChange, error)) (account.User, error) {
	var user account.User
	err := pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Find the link's user and lock them. Completions, deactivations,
		// and sign-ins for the user wait for each other here.
		state, err := readPasswordReset(ctx, queries, hash)
		if err != nil {
			return err
		}
		if state.Link != nil {
			if err := queries.LockPasswordResetUser(ctx, state.Link.UserID); err != nil {
				return fmt.Errorf("lock password reset user: %w", err)
			}

			// 2. Read again: another completion or a deactivation may have
			// deleted the link, or deactivated the user, while this waited.
			state, err = readPasswordReset(ctx, queries, hash)
			if err != nil {
				return err
			}
		}

		// 3. Let complete accept or refuse.
		change, err := complete(state)
		if err != nil {
			return err
		}
		// complete must refuse a missing link; this guards other callers.
		if state.Link == nil {
			return errNoLink
		}
		user = state.User
		userID := user.ID

		// 4. Replace the password and end everything the old one started.
		if err := queries.UpdateUserPassword(ctx, sqlc.UpdateUserPasswordParams{UserID: userID, PasswordHash: change.PasswordHash}); err != nil {
			return fmt.Errorf("replace reset password: %w", err)
		}
		if err := queries.DeleteUserSessions(ctx, userID); err != nil {
			return fmt.Errorf("end reset user sessions: %w", err)
		}
		if _, err := queries.DeleteUserSignInChallenges(ctx, userID); err != nil {
			return fmt.Errorf("remove reset user challenges: %w", err)
		}
		// Every link goes, so each one works once and an older one cannot
		// undo this reset.
		if _, err := queries.DeleteUserPasswordResetLinks(ctx, userID); err != nil {
			return fmt.Errorf("delete password reset links: %w", err)
		}
		// The failures were guesses at the old password. Saved emails and
		// throttle keys share emailaddress.Parse's form.
		if err := queries.DeleteSignInAttemptsByEmail(ctx, &user.Email); err != nil {
			return fmt.Errorf("clear reset sign-in attempts: %w", err)
		}

		// 5. Record the event. No session is needed to complete: whoever
		// holds the link acts, so the event names no actor.
		if err := queries.InsertAuditEvent(ctx, sqlc.InsertAuditEventParams{
			Action: passwordResetAction, TargetUserID: &userID,
		}); err != nil {
			return fmt.Errorf("record password reset: %w", err)
		}
		return nil
	})
	if err != nil {
		return account.User{}, err
	}
	return user, nil
}

// readPasswordReset returns the link with hash and its user, read through
// queries, or a state without a link when none has hash.
func readPasswordReset(ctx context.Context, queries *sqlc.Queries, hash [sha256.Size]byte) (passwordreset.CompleteState, error) {
	row, err := queries.ReadPasswordResetLink(ctx, hash[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return passwordreset.CompleteState{}, nil
	}
	if err != nil {
		return passwordreset.CompleteState{}, fmt.Errorf("read password reset link: %w", err)
	}
	return passwordreset.CompleteState{
		Link: &passwordreset.Link{
			TokenHash: hash, UserID: row.UserID, Email: row.Email,
			CreatedAt: row.CreatedAt.UTC(), ExpiresAt: row.ExpiresAt.UTC(),
		},
		User:        account.User{ID: row.UserID, Email: row.UserEmail, Role: account.Role(row.Role)},
		Deactivated: row.Deactivated,
	}, nil
}

// RecordPasswordResetRequest records an attempt from client at the time
// check returns, if check accepts the times of client's attempts kept after
// since, and saves the job that sends its link, to the account with email
// if any. An error from check is returned unchanged and records nothing.
func (db *DB) RecordPasswordResetRequest(ctx context.Context, client, email string, since time.Time, check func([]time.Time) (time.Time, error)) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Lock the client. The lock covers the count and the insert, so
		// the client limit counts every request, even the client's first.
		if err := queries.LockPasswordResetClient(ctx, client); err != nil {
			return fmt.Errorf("lock password reset client: %w", err)
		}

		// 2. Read the client's attempts within the window.
		times, err := queries.ListPasswordResetClientAttempts(ctx, sqlc.ListPasswordResetClientAttemptsParams{ClientPrefix: &client, Since: since})
		if err != nil {
			return fmt.Errorf("read password reset attempts: %w", err)
		}
		for i := range times {
			times[i] = times[i].UTC()
		}

		// 3. Let check accept or refuse.
		createdAt, err := check(times)
		if err != nil {
			return err
		}

		// 4. Record the attempt.
		attemptID, err := queries.InsertPasswordResetAttempt(ctx, sqlc.InsertPasswordResetAttemptParams{ClientPrefix: &client, CreatedAt: createdAt})
		if err != nil {
			return fmt.Errorf("insert password reset attempt: %w", err)
		}

		// 5. Find the account's ID, 0 for none, and save the job in this
		// transaction, so it commits or rolls back with the attempt. Every
		// request does both, so the answer takes the same work either way.
		userID, err := queries.ReadPasswordResetUserID(ctx, email)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read password reset account: %w", err)
		}
		if db.workflows == nil {
			return errNoWorkflows
		}
		if err := db.workflows.EnqueuePasswordResetLink(ctx, tx, attemptID, userID); err != nil {
			return fmt.Errorf("save password reset link job: %w", err)
		}
		return nil
	})
}

// CreatePasswordResetLink calls create with the account with userID, if
// any, and the times links were saved for it after since. If create returns
// a link, it saves it for that account and counts it: it marks the attempt
// with attemptID with the account and the link's time, or saves a row of its
// own when that attempt already counts a link or is gone. It returns
// create's error unchanged, and errNoAccount for a link without an account.
func (db *DB) CreatePasswordResetLink(ctx context.Context, attemptID, userID int64, since time.Time, create func(passwordreset.LinkState) (passwordreset.LinkChange, error)) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		var state passwordreset.LinkState

		// 1. Lock the account, if it still exists. The lock orders this
		// with the account's other requests, its completions, and its
		// deactivation.
		row, err := queries.LockPasswordResetAccount(ctx, userID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("lock password reset account: %w", err)
		}

		// 2. Read the links saved for it within the window.
		if err == nil {
			state.Account = &passwordreset.Account{
				User:     account.User{ID: row.ID, Email: row.Email, Role: account.Role(row.Role)},
				Verified: row.Verified, Deactivated: row.Deactivated,
			}
			state.LinksSent, err = queries.ListPasswordResetUserAttempts(ctx, sqlc.ListPasswordResetUserAttemptsParams{UserID: &row.ID, Since: &since})
			if err != nil {
				return fmt.Errorf("read password reset links sent: %w", err)
			}
			for i := range state.LinksSent {
				state.LinksSent[i] = state.LinksSent[i].UTC()
			}
		}

		// 3. Let create decide.
		change, err := create(state)
		if err != nil {
			return err
		}

		if change.Link == nil {
			return nil
		}
		// create must send a link only to an account; this guards other
		// callers.
		if state.Account == nil {
			return errNoAccount
		}

		// 4. Save the link. The locked account owns it, whatever create put
		// in it.
		link := change.Link
		if err := queries.InsertPasswordResetLink(ctx, sqlc.InsertPasswordResetLinkParams{
			TokenHash: link.TokenHash[:], UserID: userID, Email: state.Account.User.Email,
			CreatedAt: link.CreatedAt, ExpiresAt: link.ExpiresAt,
		}); err != nil {
			return fmt.Errorf("insert password reset link: %w", err)
		}

		// 5. Count the link for the account: mark the request's attempt with
		// the account and the link's time, or, for a later link for it or an
		// attempt the cleanup deleted, save a row of its own, so every link
		// counts. The account limits count that time, which can be well
		// after the request's when this waited for the lock.
		marked, err := queries.MarkPasswordResetAttemptLink(ctx, sqlc.MarkPasswordResetAttemptLinkParams{
			ID: attemptID, UserID: &userID, LinkedAt: &link.CreatedAt,
		})
		if err != nil {
			return fmt.Errorf("mark password reset attempt: %w", err)
		}
		if marked == 0 {
			if err := queries.InsertPasswordResetLinkAttempt(ctx, sqlc.InsertPasswordResetLinkAttemptParams{
				UserID: &userID, LinkedAt: &link.CreatedAt,
			}); err != nil {
				return fmt.Errorf("count password reset link: %w", err)
			}
		}
		return nil
	})
}

// DeleteExpiredPasswordResetLinks deletes up to limit links expiring at or
// before expiredBy, skipping links another transaction holds, and returns
// how many it deleted.
func (db *DB) DeleteExpiredPasswordResetLinks(ctx context.Context, expiredBy time.Time, limit int) (int, error) {
	deleted, err := sqlc.New(db.pool).DeleteExpiredPasswordResetLinkBatch(ctx, sqlc.DeleteExpiredPasswordResetLinkBatchParams{
		ExpiredBy: expiredBy, BatchSize: int32(min(limit, math.MaxInt32)),
	})
	if err != nil {
		return 0, fmt.Errorf("delete expired password reset links: %w", err)
	}
	return int(deleted), nil
}

// DeleteOldPasswordResetAttempts deletes up to limit attempts whose times
// are all at or before since, skipping attempts another transaction holds,
// and returns how many it deleted.
func (db *DB) DeleteOldPasswordResetAttempts(ctx context.Context, since time.Time, limit int) (int, error) {
	deleted, err := sqlc.New(db.pool).DeleteOldPasswordResetAttemptBatch(ctx, sqlc.DeleteOldPasswordResetAttemptBatchParams{
		Since: since, BatchSize: int32(min(limit, math.MaxInt32)),
	})
	if err != nil {
		return 0, fmt.Errorf("delete old password reset attempts: %w", err)
	}
	return int(deleted), nil
}
