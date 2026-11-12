package postgres

import (
	"context"
	"fmt"
	"slices"
	"uuid"

	"inbok/internal/conversation"
	"inbok/internal/mailsync"
	"inbok/internal/repository/postgres/sqlc"
)

// ListThreads returns userID's conversations of ids with their threads,
// leaving out any that is not theirs.
func (db *DB) ListThreads(ctx context.Context, userID int64, ids []uuid.UUID) ([]conversation.Thread, error) {
	rows, err := sqlc.New(db.pool).ListConversationThreads(ctx, sqlc.ListConversationThreadsParams{Ids: ids, UserID: userID})
	if err != nil {
		return nil, fmt.Errorf("list conversation threads: %w", err)
	}
	threads := make([]conversation.Thread, 0, len(rows))
	for _, row := range rows {
		threads = append(threads, conversation.Thread{ID: row.ID, AccountID: row.ConnectedAccountID, ProviderThreadID: row.ProviderThreadID})
	}
	return threads, nil
}

// UpdateMessageLabels writes updates to accountID's messages and their
// conversations through [DB.UpdateSyncedMail], keeping the sync state,
// leaving out a mailbox the account does not have yet. It returns
// [mailsync.ErrNotFound] when the account has no sync state.
//
// DESIGN: the mailboxes are read under the sync state's lock, in the
// transaction that writes, so an update left without a label is never
// written over a poll that already added that label at the same version;
// a poll after it, at the same version, adds the label back (ADR001,
// "Reading Gmail").
func (db *DB) UpdateMessageLabels(ctx context.Context, accountID int64, updates []mailsync.MessageUpdate) error {
	_, err := db.updateSyncedMail(ctx, accountID, func(queries *sqlc.Queries, current mailsync.SyncState) (mailsync.Change, error) {
		// A label made at the provider since the last sync listed the
		// mailboxes: the next poll lists it and adds the message to it.
		known, err := queries.ListMailboxProviderIDs(ctx, accountID)
		if err != nil {
			return mailsync.Change{}, fmt.Errorf("list mailboxes: %w", err)
		}
		kept := slices.Clone(updates)
		for i := range kept {
			kept[i].MailboxProviderIDs = slices.DeleteFunc(slices.Clone(kept[i].MailboxProviderIDs), func(id string) bool {
				return !slices.Contains(known, id)
			})
		}
		return mailsync.Change{State: current, Updated: kept}, nil
	})
	return err
}

// ReadConversations returns userID's conversations of ids, in the order of
// ids, leaving out any that is not theirs.
func (db *DB) ReadConversations(ctx context.Context, userID int64, ids []uuid.UUID) ([]conversation.Conversation, error) {
	return conversationRows(ctx, sqlc.New(db.pool), userID, ids)
}
