package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"amplop/internal/account"
	"amplop/internal/repository/postgres/sqlc"
	"amplop/internal/session"
)

// StartSignIn saves the session or the TOTP challenge update returns for
// userID, in one transaction. It returns [session.ErrNotFound] when the user
// does not exist.
func (db *DB) StartSignIn(ctx context.Context, userID int64, update func(session.SignInState) (session.SignInChange, error)) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Lock the user, so sign-in waits for recovery and enrollment.
		user, err := queries.LockSignInUser(ctx, userID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return session.ErrNotFound
			}
			return fmt.Errorf("lock sign-in owner: %w", err)
		}

		// 2. Read the factor and let update decide.
		factor, err := readTOTP(ctx, queries, userID)
		if err != nil {
			return err
		}
		next, err := update(session.SignInState{
			PasswordHash: user.PasswordHash, Role: account.Role(user.Role), Factor: factor, Deactivated: user.Deactivated,
		})
		if err != nil {
			return err
		}

		// 3. Delete the user's expired challenges.
		if err := queries.DeleteExpiredSignInChallenges(ctx, sqlc.DeleteExpiredSignInChallengesParams{UserID: userID, Cutoff: next.Cutoff}); err != nil {
			return fmt.Errorf("prune sign-in challenges: %w", err)
		}

		// 4. Save the session, or the challenge for the code step.
		switch next.Step {
		case session.SignInStepComplete:
			if err := deleteUserExpiredSessions(ctx, queries, userID, next.Expiry); err != nil {
				return err
			}
			err = queries.InsertSession(ctx, insertSessionParams(next.Session))
		case session.SignInStepTOTP:
			err = queries.InsertSignInChallenge(ctx, sqlc.InsertSignInChallengeParams{
				UserID: userID, TokenHash: next.Challenge.TokenHash[:], ExpiresAt: next.Challenge.ExpiresAt,
			})
		default:
			return errors.New("invalid sign-in step")
		}
		if err != nil {
			return fmt.Errorf("save sign-in proof: %w", err)
		}
		return nil
	})
}
