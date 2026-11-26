package postgres

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5"

	"amplop/internal/conversation"
	"amplop/internal/mailsync"
	"amplop/internal/repository/postgres/sqlc"
	"amplop/internal/search"
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

			ConversationMailboxIDs: row.ConversationMailboxIds,
			ConversationFlagged:    row.ConversationFlagged,
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

// RoleMailboxes returns the IDs of member owner's mailboxes that have a
// role, in every connected account, by role.
func (db *DB) RoleMailboxes(ctx context.Context, owner int64) (map[mailsync.Role][]uuid.UUID, error) {
	rows, err := sqlc.New(db.pool).ListRoleMailboxes(ctx, owner)
	if err != nil {
		return nil, fmt.Errorf("list role mailboxes: %w", err)
	}
	byRole := map[mailsync.Role][]uuid.UUID{}
	for _, row := range rows {
		byRole[mailsync.Role(row.Role)] = append(byRole[mailsync.Role(row.Role)], row.ID)
	}
	return byRole, nil
}

// LabelIDs returns those of ids that are member owner's labels, mailboxes
// of theirs with no role that they made.
func (db *DB) LabelIDs(ctx context.Context, owner int64, ids []uuid.UUID) ([]uuid.UUID, error) {
	labels, err := sqlc.New(db.pool).ListMemberLabelIDs(ctx, sqlc.ListMemberLabelIDsParams{Owner: owner, Ids: ids})
	if err != nil {
		return nil, fmt.Errorf("list labels: %w", err)
	}
	return labels, nil
}

// FoundMessages returns member owner's messages among ids that have a
// conversation, each with its conversation as lists show it and the roles
// of its mailboxes, in no set order. It reads both from one snapshot, so
// each message's roles and conversation agree.
func (db *DB) FoundMessages(ctx context.Context, owner int64, ids []uuid.UUID) ([]search.Stored, error) {
	var found []search.Stored
	options := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	err := pgx.BeginTxFunc(ctx, db.pool, options, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		rows, err := queries.ListFoundMessages(ctx, sqlc.ListFoundMessagesParams{Owner: owner, Ids: ids})
		if err != nil {
			return fmt.Errorf("list found messages: %w", err)
		}
		conversationIDs := make([]uuid.UUID, len(rows))
		for n, row := range rows {
			conversationIDs[n] = row.ConversationID
		}
		conversations, err := conversationRows(ctx, queries, owner, conversationIDs)
		if err != nil {
			return err
		}
		byID := make(map[uuid.UUID]conversation.Conversation, len(conversations))
		for _, c := range conversations {
			byID[c.ID] = c
		}

		found = make([]search.Stored, 0, len(rows))
		for _, row := range rows {
			from, err := decodeAddresses(row.FromAddresses)
			if err != nil {
				return err
			}
			roles := make([]mailsync.Role, len(row.MailboxRoles))
			for n, role := range row.MailboxRoles {
				roles[n] = mailsync.Role(role)
			}
			found = append(found, search.Stored{
				Message: search.Message{
					ID:        row.ID,
					AccountID: row.ConnectedAccountID,
					Date:      row.InternalDate.UTC(),
					From:      from,
					Subject:   row.Subject,
					Preview:   row.Preview,
				},
				Roles:        roles,
				Conversation: byID[row.ConversationID],
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return found, nil
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
