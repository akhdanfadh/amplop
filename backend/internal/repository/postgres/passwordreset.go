package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"inbok/internal/account"
	"inbok/internal/passwordreset"
	"inbok/internal/repository/postgres/sqlc"
)

const passwordResetAction = "password-reset"

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
