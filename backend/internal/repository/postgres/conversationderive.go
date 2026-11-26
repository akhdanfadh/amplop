package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"uuid"

	"github.com/jackc/pgx/v5"

	"inbok/internal/conversation"
	"inbok/internal/mailsync"
	"inbok/internal/repository/postgres/sqlc"
)

// errCountRange means a conversation has more messages than an integer
// column holds.
var errCountRange = errors.New("conversation's message count is out of storage's range")

// deriveConversations makes the conversations of accountID's threads match
// their messages, in tx, as [conversation.Derive] makes them: a thread with
// none to show loses its conversation, and every other one gets its row and
// mailboxes again, keeping its ID. It returns the IDs of every message the
// threads have, whose search documents hold their conversation's places. It
// must run in the transaction that wrote the messages, after every write to
// them.
//
// DESIGN: Conversations are a summary the list reads with one index, kept
// only by this function. Every write to the mail tables goes through
// [DB.UpdateSyncedMail], which calls it for the threads it touched, so the
// summary is right after every write inbok makes. SQL run by hand, or a write
// added elsewhere, leaves it stale. A database trigger would cover such
// writes, but it would fire several times per sync step, some with
// memberships half replaced, and move the rules into PL/pgSQL, which inbok
// uses nowhere else.
func deriveConversations(ctx context.Context, tx pgx.Tx, accountID int64, threads []string) ([]uuid.UUID, error) {
	if len(threads) == 0 {
		return nil, nil
	}
	queries := sqlc.New(tx)

	// 1. Read the threads' messages, and derive each thread's conversation.
	rows, err := queries.ListThreadMessages(ctx, sqlc.ListThreadMessagesParams{ConnectedAccountID: accountID, ProviderThreadIds: threads})
	if err != nil {
		return nil, fmt.Errorf("list thread messages: %w", err)
	}
	byThread := make(map[string][]conversation.Message, len(threads))
	messageIDs := make([]uuid.UUID, len(rows))
	for n, row := range rows {
		message, err := derivedMessageFromRow(row)
		if err != nil {
			return nil, err
		}
		byThread[row.ProviderThreadID] = append(byThread[row.ProviderThreadID], message)
		messageIDs[n] = row.ID
	}
	var gone []string
	var saves []sqlc.UpsertConversationParams
	var places [][]uuid.UUID
	for _, thread := range threads {
		summary, ok := conversation.Derive(byThread[thread])
		if !ok {
			gone = append(gone, thread)
			continue
		}
		params, err := conversationParams(accountID, thread, summary)
		if err != nil {
			return nil, err
		}
		saves = append(saves, params)
		places = append(places, summary.Mailboxes)
	}

	// 2. Drop the conversations left with nothing to show.
	if len(gone) > 0 {
		if err := queries.DeleteThreadConversations(ctx, sqlc.DeleteThreadConversationsParams{ConnectedAccountID: accountID, ProviderThreadIds: gone}); err != nil {
			return nil, fmt.Errorf("delete conversations: %w", err)
		}
	}
	if len(saves) == 0 {
		return messageIDs, nil
	}

	// 3. Save the rest's rows, which keep their IDs.
	ids := make([]uuid.UUID, len(saves))
	var batchErr error
	queries.UpsertConversation(ctx, saves).QueryRow(func(i int, id uuid.UUID, err error) {
		ids[i] = id
		batchErr = errors.Join(batchErr, err)
	})
	if batchErr != nil {
		return nil, fmt.Errorf("save conversations: %w", batchErr)
	}

	// 4. Replace their mailboxes, which copy the rows just saved.
	if err := queries.DeleteConversationMailboxes(ctx, ids); err != nil {
		return nil, fmt.Errorf("delete conversation mailboxes: %w", err)
	}
	pairs := sqlc.InsertConversationMailboxesParams{ConnectedAccountID: accountID}
	for i, mailboxes := range places {
		for _, mailbox := range mailboxes {
			pairs.ConversationIds = append(pairs.ConversationIds, ids[i])
			pairs.MailboxIds = append(pairs.MailboxIds, mailbox)
		}
	}
	if len(pairs.ConversationIds) == 0 {
		return messageIDs, nil
	}
	if err := queries.InsertConversationMailboxes(ctx, pairs); err != nil {
		return nil, fmt.Errorf("insert conversation mailboxes: %w", err)
	}
	return messageIDs, nil
}

// derivedMessageFromRow converts a thread message as storage read it.
func derivedMessageFromRow(row sqlc.ListThreadMessagesRow) (conversation.Message, error) {
	from, err := decodeAddresses(row.FromAddresses)
	if err != nil {
		return conversation.Message{}, err
	}
	mailboxes := make([]conversation.Mailbox, len(row.MailboxIds))
	for i, id := range row.MailboxIds {
		mailboxes[i] = conversation.Mailbox{ID: id, Role: mailsync.Role(row.MailboxRoles[i])}
	}
	return conversation.Message{
		ID:        row.ID,
		Date:      row.InternalDate.UTC(),
		Subject:   row.Subject,
		Preview:   row.Preview,
		From:      from,
		Seen:      row.Seen,
		Flagged:   row.Flagged,
		Mailboxes: mailboxes,
	}, nil
}

// conversationParams returns summary as the upsert's parameters for
// accountID's thread.
func conversationParams(accountID int64, thread string, summary conversation.Summary) (sqlc.UpsertConversationParams, error) {
	latestFrom, err := encodeAddresses(summary.LatestFrom)
	if err != nil {
		return sqlc.UpsertConversationParams{}, err
	}
	participants, err := encodeAddresses(summary.Participants)
	if err != nil {
		return sqlc.UpsertConversationParams{}, err
	}
	if summary.MessageCount > math.MaxInt32 {
		return sqlc.UpsertConversationParams{}, errCountRange
	}
	return sqlc.UpsertConversationParams{
		ConnectedAccountID: accountID,
		ProviderThreadID:   thread,
		LatestDate:         summary.LatestDate,
		Subject:            summary.Subject,
		Preview:            summary.Preview,
		LatestFrom:         latestFrom,
		Participants:       participants,
		MessageCount:       int32(summary.MessageCount),
		Unread:             summary.Unread,
		Flagged:            summary.Flagged,
		InAllMail:          summary.InAllMail,
		NewestMessageID:    summary.NewestMessageID,
	}, nil
}

// decodeAddresses reads an address list as messages keep them.
func decodeAddresses(raw []byte) ([]mailsync.Address, error) {
	var stored []storedAddress
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("decode addresses: %w", err)
	}
	addresses := make([]mailsync.Address, 0, len(stored))
	for _, a := range stored {
		addresses = append(addresses, mailsync.Address(a))
	}
	return addresses, nil
}
