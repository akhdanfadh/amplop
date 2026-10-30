package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"inbok/internal/repository/postgres/sqlc"
	"inbok/internal/session"
	"inbok/internal/totp"
)

// StartSignIn serializes password sign-in with enrollment confirmation.
func (db *DB) StartSignIn(ctx context.Context, userID int64, update func(*totp.Factor) (session.SignInChange, error)) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		if _, err := queries.LockTOTPUser(ctx, userID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return session.ErrNotFound
			}
			return fmt.Errorf("lock sign-in owner: %w", err)
		}
		factor, err := readTOTP(ctx, queries, userID)
		if err != nil {
			return err
		}
		next, err := update(factor)
		if err != nil {
			return err
		}

		if err := queries.DeleteExpiredSignInChallenges(ctx, sqlc.DeleteExpiredSignInChallengesParams{UserID: userID, Cutoff: next.Cutoff}); err != nil {
			return fmt.Errorf("prune sign-in challenges: %w", err)
		}
		switch next.Step {
		case session.SignInStepComplete:
			err = queries.InsertSession(ctx, sqlc.InsertSessionParams{
				UserID: userID, TokenHash: next.Session.TokenHash[:], CreatedAt: next.Session.CreatedAt,
				LastUsedAt: next.Session.LastUsedAt, ExpiresAt: next.Session.ExpiresAt,
			})
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
