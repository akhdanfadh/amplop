package postgres

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5"

	"inbok/internal/mailsync"
	"inbok/internal/repository/postgres/sqlc"
	"inbok/internal/search"
)

// IndexSources returns member owner's messages among ids, with what their
// search documents need, in no set order. An ID owner has no message
// with is left out.
func (db *DB) IndexSources(ctx context.Context, owner int64, ids []uuid.UUID) ([]search.Source, error) {
	rows, err := sqlc.New(db.pool).ListIndexSources(ctx, sqlc.ListIndexSourcesParams{Owner: owner, Ids: ids})
	if err != nil {
		return nil, fmt.Errorf("list index sources: %w", err)
	}
	sources := make([]search.Source, 0, len(rows))
	for _, row := range rows {
		source := search.Source{
			ID:           row.ID,
			AccountID:    row.ConnectedAccountID,
			ThreadID:     row.ProviderThreadID,
			InternalDate: row.InternalDate.UTC(),
			Subject:      row.Subject,
			MailboxIDs:   row.MailboxIds,
			HasParts:     row.HasParts,
		}
		lists := []*[]mailsync.Address{&source.From, &source.To, &source.CC}
		for i, raw := range [][]byte{row.FromAddresses, row.ToAddresses, row.CcAddresses} {
			if *lists[i], err = decodeAddresses(raw); err != nil {
				return nil, err
			}
		}
		sources = append(sources, source)
	}
	return sources, nil
}

// UserIDs returns the ID of every member, with mail or not.
func (db *DB) UserIDs(ctx context.Context) ([]int64, error) {
	ids, err := sqlc.New(db.pool).ListUserIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list user IDs: %w", err)
	}
	return ids, nil
}

// UserExists reports whether member owner exists.
func (db *DB) UserExists(ctx context.Context, owner int64) (bool, error) {
	exists, err := sqlc.New(db.pool).UserExists(ctx, owner)
	if err != nil {
		return false, fmt.Errorf("check user: %w", err)
	}
	return exists, nil
}

// MessageIDs returns the IDs of all of member owner's messages, in every
// connected account.
func (db *DB) MessageIDs(ctx context.Context, owner int64) ([]uuid.UUID, error) {
	ids, err := sqlc.New(db.pool).ListUserMessageIDs(ctx, owner)
	if err != nil {
		return nil, fmt.Errorf("list user message IDs: %w", err)
	}
	return ids, nil
}

// OwnsAccount reports whether member owner has connected account
// accountID.
func (db *DB) OwnsAccount(ctx context.Context, owner, accountID int64) (bool, error) {
	user, err := sqlc.New(db.pool).ReadAccountOwner(ctx, accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read account owner: %w", err)
	}
	return user == owner, nil
}

// MailboxIDs returns the IDs of member owner's mailboxes with any of
// roles, in every connected account.
func (db *DB) MailboxIDs(ctx context.Context, owner int64, roles []mailsync.Role) ([]uuid.UUID, error) {
	ids, err := sqlc.New(db.pool).ListMailboxIDsWithRoles(ctx, sqlc.ListMailboxIDsWithRolesParams{Owner: owner, Roles: roleTexts(roles)})
	if err != nil {
		return nil, fmt.Errorf("list mailboxes with roles: %w", err)
	}
	return ids, nil
}

// FoundMessages returns member owner's messages among ids that are in no
// mailbox with any of hidden's roles, in no set order.
func (db *DB) FoundMessages(ctx context.Context, owner int64, ids []uuid.UUID, hidden []mailsync.Role) ([]search.Message, error) {
	rows, err := sqlc.New(db.pool).ListFoundMessages(ctx, sqlc.ListFoundMessagesParams{Owner: owner, Ids: ids, Roles: roleTexts(hidden)})
	if err != nil {
		return nil, fmt.Errorf("list found messages: %w", err)
	}
	messages := make([]search.Message, 0, len(rows))
	for _, row := range rows {
		from, err := decodeAddresses(row.FromAddresses)
		if err != nil {
			return nil, err
		}
		messages = append(messages, search.Message{
			ID:        row.ID,
			AccountID: row.ConnectedAccountID,
			Date:      row.InternalDate.UTC(),
			From:      from,
			Subject:   row.Subject,
			Preview:   row.Preview,
			Unread:    !row.Seen,
			Starred:   row.Flagged,
		})
	}
	return messages, nil
}

// indexAfter saves, in tx, the job that brings accountID's messages with
// ids in the search index up to date with what tx commits. A worker sees
// it only once tx commits, and a rollback drops it with the change.
func (db *DB) indexAfter(ctx context.Context, tx pgx.Tx, accountID int64, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	if db.workflows == nil {
		return errNoWorkflows
	}
	owner, err := sqlc.New(tx).ReadAccountOwner(ctx, accountID)
	if err != nil {
		return fmt.Errorf("read account owner: %w", err)
	}
	if err := db.workflows.EnqueueSearchIndexing(ctx, tx, owner, ids); err != nil {
		return fmt.Errorf("save search indexing: %w", err)
	}
	return nil
}
