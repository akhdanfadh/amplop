package postgres

import (
	"context"
	"fmt"
	"math"
	"time"
	"uuid"

	"inbok/internal/mailsync"
	"inbok/internal/messagecontent"
	"inbok/internal/repository/postgres/sqlc"
)

// AccountsWithDueBodies returns, in ID order, the accounts whose member
// is active, which need no reconnect, whose full sync is complete, and for
// which [DB.MessagesNeedingBodies] lists at least one message for filter.
func (db *DB) AccountsWithDueBodies(ctx context.Context, filter messagecontent.DueFilter) ([]int64, error) {
	accounts, err := sqlc.New(db.pool).ListAccountsWithDueBodies(ctx, sqlc.ListAccountsWithDueBodiesParams{
		Since: filter.Since, FailedSince: filter.FailedSince, ExcludedRoles: roleTexts(filter.ExcludedRoles),
	})
	if err != nil {
		return nil, fmt.Errorf("list accounts with due bodies: %w", err)
	}
	return accounts, nil
}

// MessagesNeedingBodies returns up to limit of account accountID's
// messages without parts that filter keeps, newest first.
func (db *DB) MessagesNeedingBodies(ctx context.Context, accountID int64, filter messagecontent.DueFilter, limit int) ([]messagecontent.PendingBody, error) {
	rows, err := sqlc.New(db.pool).ListMessagesNeedingBodies(ctx, sqlc.ListMessagesNeedingBodiesParams{
		ConnectedAccountID: accountID,
		Since:              filter.Since,
		FailedSince:        filter.FailedSince,
		ExcludedRoles:      roleTexts(filter.ExcludedRoles),
		MaxMessages:        int32(min(limit, math.MaxInt32)),
	})
	if err != nil {
		return nil, fmt.Errorf("list messages needing bodies: %w", err)
	}
	pending := make([]messagecontent.PendingBody, len(rows))
	for i, row := range rows {
		pending[i] = messagecontent.PendingBody{ID: row.ID, ProviderID: row.ProviderMessageID}
	}
	return pending, nil
}

// roleTexts returns roles as the text mailboxes.role holds, never nil, so
// an empty list excludes nothing rather than reading as NULL.
func roleTexts(roles []mailsync.Role) []string {
	texts := make([]string, len(roles))
	for i, role := range roles {
		texts[i] = string(role)
	}
	return texts
}

// MarkBodyFailed records at as when fetching account accountID's message
// messageID's body last failed for good, in one transaction that first
// locks the account's sync state. It returns
// [messagecontent.ErrMessageNotFound] when the account has no such
// message.
func (db *DB) MarkBodyFailed(ctx context.Context, accountID int64, messageID uuid.UUID, at time.Time) error {
	return db.withSyncStateLock(ctx, accountID, func(queries *sqlc.Queries) error {
		marked, err := queries.MarkMessageBodyFailed(ctx, sqlc.MarkMessageBodyFailedParams{
			FailedAt: &at, ConnectedAccountID: accountID, ID: messageID,
		})
		if err != nil {
			return fmt.Errorf("mark message body failed: %w", err)
		}
		if marked == 0 {
			return messagecontent.ErrMessageNotFound
		}
		return nil
	})
}
