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
	"inbok/internal/emailverification"
	"inbok/internal/repository/postgres/sqlc"
)

// RequestEmailVerification implements [emailverification.Storage.RequestEmailVerification].
func (db *DB) RequestEmailVerification(ctx context.Context, userID int64, create func(emailverification.RequestState) (emailverification.RequestChange, error)) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Lock the user.
		user, err := queries.LockEmailVerificationUser(ctx, userID)
		// A deactivation ended the session this request was checked with.
		if errors.Is(err, pgx.ErrNoRows) || user.Deactivated {
			return account.ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("lock email verification user: %w", err)
		}

		// 2. Read the user's link times and whether system email is set up.
		createdAt, err := queries.ListEmailVerificationLinkTimes(ctx, userID)
		if err != nil {
			return fmt.Errorf("read email verification link times: %w", err)
		}
		configured, err := queries.HasSystemEmail(ctx)
		if err != nil {
			return fmt.Errorf("read system email: %w", err)
		}

		// 3. Let create accept or refuse.
		change, err := create(emailverification.RequestState{
			Email:          user.Email,
			Verified:       user.EmailVerifiedAt != nil,
			MailConfigured: configured,
			LinksCreatedAt: createdAt,
		})
		if err != nil {
			return err
		}

		// 4. Delete the user's expired links.
		if err := queries.DeleteExpiredEmailVerificationLinks(ctx, sqlc.DeleteExpiredEmailVerificationLinksParams{
			UserID: userID, ExpiredBy: change.ExpiredBy,
		}); err != nil {
			return fmt.Errorf("delete expired email verification links: %w", err)
		}
		link := change.Link

		// 5. Save the link. The locked user owns it, whatever create put in
		// it.
		if err := queries.InsertEmailVerificationLink(ctx, sqlc.InsertEmailVerificationLinkParams{
			TokenHash: link.TokenHash[:],
			UserID:    userID,
			Email:     link.Email,
			CreatedAt: link.CreatedAt,
			ExpiresAt: link.ExpiresAt,
		}); err != nil {
			return fmt.Errorf("insert email verification link: %w", err)
		}
		return nil
	})
}

const emailVerifiedAction = "email-verified"

// RedeemEmailVerification implements [emailverification.Storage.RedeemEmailVerification].
func (db *DB) RedeemEmailVerification(ctx context.Context, hash [sha256.Size]byte, redeem func(emailverification.RedeemState) (emailverification.RedeemChange, error)) error {
	return pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Find the link's user.
		found, err := queries.ReadEmailVerificationLink(ctx, hash[:])
		if errors.Is(err, pgx.ErrNoRows) {
			return emailverification.ErrLinkInvalid
		}
		if err != nil {
			return fmt.Errorf("read email verification link: %w", err)
		}

		// 2. Lock the user.
		user, err := queries.LockEmailVerificationUser(ctx, found.UserID)
		// Deleting the user while this waited also deleted the link.
		// Deactivating deletes links too, so the flag check only guards
		// against a link that somehow outlived it.
		if errors.Is(err, pgx.ErrNoRows) || user.Deactivated {
			return emailverification.ErrLinkInvalid
		}
		if err != nil {
			return fmt.Errorf("lock email verification user: %w", err)
		}

		// 3. Read the link again: another redeem of this user may have deleted
		// it while this one waited for the lock.
		row, err := queries.ReadEmailVerificationLink(ctx, hash[:])
		if errors.Is(err, pgx.ErrNoRows) {
			return emailverification.ErrLinkInvalid
		}
		if err != nil {
			return fmt.Errorf("read email verification link: %w", err)
		}

		// 4. Let redeem accept or refuse.
		change, err := redeem(emailverification.RedeemState{
			Link: emailverification.Link{
				TokenHash: hash,
				UserID:    row.UserID,
				Email:     row.Email,
				CreatedAt: row.CreatedAt.UTC(),
				ExpiresAt: row.ExpiresAt.UTC(),
			},
			Email:    user.Email,
			Verified: user.EmailVerifiedAt != nil,
		})
		if err != nil {
			return err
		}

		// 5. Save the verified time and record the event, unless the email
		// was already verified.
		if change.VerifiedAt != nil {
			if err := queries.UpdateUserEmailVerifiedAt(ctx, sqlc.UpdateUserEmailVerifiedAtParams{
				UserID: row.UserID, EmailVerifiedAt: change.VerifiedAt,
			}); err != nil {
				return fmt.Errorf("save email verified time: %w", err)
			}
			// No session is needed to redeem: whoever holds the link acts, so
			// the event names no actor.
			if err := queries.InsertAuditEvent(ctx, sqlc.InsertAuditEventParams{
				Action: emailVerifiedAction, TargetUserID: &row.UserID,
			}); err != nil {
				return fmt.Errorf("record email verification: %w", err)
			}
		}

		// 6. Delete the user's links.
		if err := queries.DeleteUserEmailVerificationLinks(ctx, row.UserID); err != nil {
			return fmt.Errorf("delete email verification links: %w", err)
		}
		return nil
	})
}

// DeleteExpiredEmailVerificationLinks deletes up to limit links of any user
// expiring at or before expiredBy, skipping links another transaction
// holds, and returns how many it deleted.
func (db *DB) DeleteExpiredEmailVerificationLinks(ctx context.Context, expiredBy time.Time, limit int) (int, error) {
	deleted, err := sqlc.New(db.pool).DeleteExpiredEmailVerificationLinkBatch(ctx, sqlc.DeleteExpiredEmailVerificationLinkBatchParams{
		ExpiredBy: expiredBy, BatchSize: int32(min(limit, math.MaxInt32)),
	})
	if err != nil {
		return 0, fmt.Errorf("delete expired email verification links: %w", err)
	}
	return int(deleted), nil
}
