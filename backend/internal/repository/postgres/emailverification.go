package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"inbok/internal/account"
	"inbok/internal/emailverification"
	"inbok/internal/repository/postgres/sqlc"
)

var _ emailverification.Storage = (*DB)(nil)

// RequestEmailVerification implements
// [emailverification.Storage.RequestEmailVerification]: it locks the user,
// reads their address, verified state, link times, and whether system email
// is saved, then calls create. On success it deletes the user's expired links
// and inserts the new one, in the same transaction.
func (db *DB) RequestEmailVerification(ctx context.Context, userID int64, create func(emailverification.RequestState) (emailverification.RequestChange, error)) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		user, err := queries.LockEmailVerificationUser(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return account.ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("lock email verification user: %w", err)
		}
		createdAt, err := queries.ListEmailVerificationLinkTimes(ctx, userID)
		if err != nil {
			return fmt.Errorf("read email verification link times: %w", err)
		}
		configured, err := queries.HasSystemEmail(ctx)
		if err != nil {
			return fmt.Errorf("read system email: %w", err)
		}

		change, err := create(emailverification.RequestState{
			Email:          user.Email,
			Verified:       user.EmailVerifiedAt != nil,
			MailConfigured: configured,
			LinksCreatedAt: createdAt,
		})
		if err != nil {
			return err
		}

		if err := queries.DeleteExpiredEmailVerificationLinks(ctx, sqlc.DeleteExpiredEmailVerificationLinksParams{
			UserID: userID, ExpiredBy: change.ExpiredBy,
		}); err != nil {
			return fmt.Errorf("delete expired email verification links: %w", err)
		}
		link := change.Link
		// The locked user owns the link, whatever create put in it.
		if err := queries.InsertEmailVerificationLink(ctx, sqlc.InsertEmailVerificationLinkParams{
			TokenHash: link.TokenHash[:],
			UserID:    userID,
			Address:   link.Address,
			CreatedAt: link.CreatedAt,
			ExpiresAt: link.ExpiresAt,
		}); err != nil {
			return fmt.Errorf("insert email verification link: %w", err)
		}
		return nil
	})
}
