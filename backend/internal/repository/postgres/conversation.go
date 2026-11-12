package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"

	"inbok/internal/conversation"
	"inbok/internal/mailsync"
	"inbok/internal/repository/postgres/sqlc"
)

// newest is the cursor a first page starts from: after every ID of a date
// later than any provider's timestamp, Gmail's being milliseconds since
// 1970. A message dated on 31 December 9999 or later would be left out.
var newest = conversation.Cursor{Date: time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC), ID: uuid.Max()}

// viewRoles are the views that are each account's mailboxes of a role.
var viewRoles = map[conversation.View]mailsync.Role{
	conversation.ViewInbox: mailsync.RoleInbox,
	conversation.ViewSent:  mailsync.RoleSent,
	conversation.ViewSpam:  mailsync.RoleJunk,
	conversation.ViewTrash: mailsync.RoleTrash,
}

// readOnly is how conversation reads run: their several statements see one
// snapshot, so a page's IDs and rows agree.
var readOnly = pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}

// ListConversations returns up to query.Limit of userID's conversations in
// query.Place, newest first, then by ID descending, each older than
// query.Before. It returns [conversation.ErrAccountNotFound] when userID has
// no account query.Account, and [conversation.ErrPlaceNotFound] when userID
// has no mailbox query.Place.Mailbox.
func (db *DB) ListConversations(ctx context.Context, userID int64, query conversation.Query) ([]conversation.Conversation, error) {
	before := newest
	if query.Before != nil {
		before = *query.Before
	}
	var listed []conversation.Conversation
	err := pgx.BeginTxFunc(ctx, db.pool, readOnly, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Find the page's IDs in the place's own index.
		scope, err := placeScope(ctx, queries, userID, query.Place, query.Account)
		if err != nil {
			return err
		}
		ids, err := scope.page(ctx, queries, before, int32(query.Limit))
		if err != nil {
			return err
		}

		// 2. Read their rows, in the page's order.
		listed, err = conversationRows(ctx, queries, userID, ids)
		return err
	})
	if err != nil {
		return nil, err
	}
	return listed, nil
}

// CountConversationsAfter returns how many of userID's conversations in
// place, of account or [conversation.EveryAccount], are dated after after,
// with the errors of [DB.ListConversations].
func (db *DB) CountConversationsAfter(ctx context.Context, userID int64, place conversation.Place, account int64, after time.Time) (int, error) {
	var count int32
	err := pgx.BeginTxFunc(ctx, db.pool, readOnly, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		scope, err := placeScope(ctx, queries, userID, place, account)
		if err != nil {
			return err
		}
		count, err = scope.countAfter(ctx, queries, after)
		return err
	})
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

// ListPlaceCounts returns how many conversations of each place of userID's
// accounts are unread, in a stable order: the accounts in the order they
// were connected, each with every view, then its own labels by name.
func (db *DB) ListPlaceCounts(ctx context.Context, userID int64) ([]conversation.PlaceCount, error) {
	var counts []conversation.PlaceCount
	err := pgx.BeginTxFunc(ctx, db.pool, readOnly, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		accounts, err := queries.ListConversationUnreadCounts(ctx, userID)
		if err != nil {
			return fmt.Errorf("count unread conversations: %w", err)
		}
		roles, err := queries.ListMailboxRoleUnreadCounts(ctx, userID)
		if err != nil {
			return fmt.Errorf("count unread conversations by role: %w", err)
		}
		labels, err := queries.ListMailboxLabelUnreadCounts(ctx, userID)
		if err != nil {
			return fmt.Errorf("count unread label conversations: %w", err)
		}

		byRole := map[int64]map[mailsync.Role]int{}
		for _, role := range roles {
			if byRole[role.ConnectedAccountID] == nil {
				byRole[role.ConnectedAccountID] = map[mailsync.Role]int{}
			}
			byRole[role.ConnectedAccountID][mailsync.Role(role.Role)] = int(role.Unread)
		}
		byAccount := map[int64][]conversation.PlaceCount{}
		for _, label := range labels {
			byAccount[label.ConnectedAccountID] = append(byAccount[label.ConnectedAccountID], conversation.PlaceCount{
				Account: label.ConnectedAccountID, Place: conversation.Place{Mailbox: label.ID}, Name: label.Name, Unread: int(label.Unread),
			})
		}
		for _, account := range accounts {
			for _, view := range conversation.Views {
				unread := byRole[account.ID][viewRoles[view]]
				switch view {
				case conversation.ViewAll:
					unread = int(account.AllMail)
				case conversation.ViewStarred:
					unread = int(account.Starred)
				}
				counts = append(counts, conversation.PlaceCount{Account: account.ID, Place: conversation.Place{View: view}, Unread: unread})
			}
			counts = append(counts, byAccount[account.ID]...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return counts, nil
}

// ReadConversation returns userID's conversation id and every message of
// its thread, drafts too, oldest first, each with its mailboxes by name, or
// [conversation.ErrNotFound].
func (db *DB) ReadConversation(ctx context.Context, userID int64, id uuid.UUID) (conversation.Conversation, []conversation.Message, error) {
	var read conversation.Conversation
	var messages []conversation.Message
	err := pgx.BeginTxFunc(ctx, db.pool, readOnly, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Read the row, which checks it is the member's.
		rows, err := conversationRows(ctx, queries, userID, []uuid.UUID{id})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return conversation.ErrNotFound
		}
		read = rows[0]

		// 2. Read its messages.
		found, err := queries.ListConversationMessages(ctx, id)
		if err != nil {
			return fmt.Errorf("list conversation messages: %w", err)
		}
		messages = make([]conversation.Message, 0, len(found))
		for _, row := range found {
			message, err := conversationMessageFromRow(row)
			if err != nil {
				return err
			}
			messages = append(messages, message)
		}
		return nil
	})
	if err != nil {
		return conversation.Conversation{}, nil, err
	}
	return read, messages, nil
}

// scope is what a place lists for a member: the mailboxes whose index
// lists it, or the accounts whose All mail or Starred does.
type scope struct {
	view      conversation.View // ViewAll or ViewStarred when mailboxes is nil
	mailboxes []uuid.UUID
	accounts  []int64
}

// placeScope returns what place lists for userID's account, or every
// account for [conversation.EveryAccount], with the errors of
// [DB.ListConversations].
func placeScope(ctx context.Context, queries *sqlc.Queries, userID int64, place conversation.Place, account int64) (scope, error) {
	accounts, err := queries.ListMemberConnectedAccountIDs(ctx, sqlc.ListMemberConnectedAccountIDsParams{UserID: userID, AccountID: account})
	if err != nil {
		return scope{}, fmt.Errorf("list member accounts: %w", err)
	}
	if account != conversation.EveryAccount && len(accounts) == 0 {
		return scope{}, conversation.ErrAccountNotFound
	}

	if place.View == "" {
		owner, err := queries.ReadMemberLabelAccount(ctx, sqlc.ReadMemberLabelAccountParams{ID: place.Mailbox, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return scope{}, conversation.ErrPlaceNotFound
		}
		if err != nil {
			return scope{}, fmt.Errorf("read mailbox: %w", err)
		}
		if account != conversation.EveryAccount && owner != account {
			return scope{mailboxes: []uuid.UUID{}}, nil
		}
		return scope{mailboxes: []uuid.UUID{place.Mailbox}}, nil
	}

	role, ok := viewRoles[place.View]
	if !ok {
		return scope{view: place.View, accounts: accounts}, nil
	}
	mailboxes, err := queries.ListRoleMailboxIDs(ctx, sqlc.ListRoleMailboxIDsParams{AccountIds: accounts, Role: string(role)})
	if err != nil {
		return scope{}, fmt.Errorf("list %s mailboxes: %w", role, err)
	}
	return scope{mailboxes: orNone(mailboxes)}, nil
}

// orNone returns ids, or an empty list for nil, so a scope with no mailbox
// still lists from mailboxes.
func orNone(ids []uuid.UUID) []uuid.UUID {
	if ids == nil {
		return []uuid.UUID{}
	}
	return ids
}

// page returns the IDs of up to limit of the scope's conversations older
// than before, newest first.
func (s scope) page(ctx context.Context, queries *sqlc.Queries, before conversation.Cursor, limit int32) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	switch {
	case s.mailboxes != nil:
		rows, err := queries.ListMailboxConversationIDs(ctx, sqlc.ListMailboxConversationIDsParams{
			MailboxIds: s.mailboxes, BeforeDate: before.Date, BeforeID: before.ID, MaxRows: limit,
		})
		if err != nil {
			return nil, fmt.Errorf("list mailbox conversations: %w", err)
		}
		for _, row := range rows {
			ids = append(ids, row.ConversationID)
		}
	case s.view == conversation.ViewStarred:
		rows, err := queries.ListStarredConversationIDs(ctx, sqlc.ListStarredConversationIDsParams{
			AccountIds: s.accounts, BeforeDate: before.Date, BeforeID: before.ID, MaxRows: limit,
		})
		if err != nil {
			return nil, fmt.Errorf("list starred conversations: %w", err)
		}
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
	default:
		rows, err := queries.ListAllMailConversationIDs(ctx, sqlc.ListAllMailConversationIDsParams{
			AccountIds: s.accounts, BeforeDate: before.Date, BeforeID: before.ID, MaxRows: limit,
		})
		if err != nil {
			return nil, fmt.Errorf("list all mail conversations: %w", err)
		}
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
	}
	return ids, nil
}

// countAfter returns how many of the scope's conversations are dated after
// after.
func (s scope) countAfter(ctx context.Context, queries *sqlc.Queries, after time.Time) (int32, error) {
	var count int32
	var err error
	switch {
	case s.mailboxes != nil:
		count, err = queries.CountMailboxConversationsAfter(ctx, sqlc.CountMailboxConversationsAfterParams{MailboxIds: s.mailboxes, After: after})
	case s.view == conversation.ViewStarred:
		count, err = queries.CountStarredConversationsAfter(ctx, sqlc.CountStarredConversationsAfterParams{AccountIds: s.accounts, After: after})
	default:
		count, err = queries.CountAllMailConversationsAfter(ctx, sqlc.CountAllMailConversationsAfterParams{AccountIds: s.accounts, After: after})
	}
	if err != nil {
		return 0, fmt.Errorf("count conversations: %w", err)
	}
	return count, nil
}

// conversationRows returns userID's conversations of ids, in the order of
// ids, leaving out any that is not the member's.
func conversationRows(ctx context.Context, queries *sqlc.Queries, userID int64, ids []uuid.UUID) ([]conversation.Conversation, error) {
	if len(ids) == 0 {
		return []conversation.Conversation{}, nil
	}
	rows, err := queries.ReadConversationRows(ctx, sqlc.ReadConversationRowsParams{Ids: ids, UserID: userID})
	if err != nil {
		return nil, fmt.Errorf("read conversations: %w", err)
	}
	byID := make(map[uuid.UUID]conversation.Conversation, len(rows))
	for _, row := range rows {
		c, err := conversationFromRow(row)
		if err != nil {
			return nil, err
		}
		byID[c.ID] = c
	}
	ordered := make([]conversation.Conversation, 0, len(rows))
	for _, id := range ids {
		if c, ok := byID[id]; ok {
			ordered = append(ordered, c)
		}
	}
	return ordered, nil
}

func conversationFromRow(row sqlc.ReadConversationRowsRow) (conversation.Conversation, error) {
	latestFrom, err := decodeAddresses(row.LatestFrom)
	if err != nil {
		return conversation.Conversation{}, err
	}
	participants, err := decodeAddresses(row.Participants)
	if err != nil {
		return conversation.Conversation{}, err
	}
	return conversation.Conversation{
		ID:           row.ID,
		AccountID:    row.ConnectedAccountID,
		LatestDate:   row.LatestDate.UTC(),
		Subject:      row.Subject,
		Preview:      row.Preview,
		LatestFrom:   latestFrom,
		Participants: participants,
		MessageCount: int(row.MessageCount),
		Unread:       row.Unread,
		Flagged:      row.Flagged,
		InInbox:      row.InInbox,
		InSent:       row.InSent,
		InSpam:       row.InSpam,
		InTrash:      row.InTrash,
		InAllMail:    row.InAllMail,
		LabelIDs:     row.LabelIds,
	}, nil
}

func conversationMessageFromRow(row sqlc.ListConversationMessagesRow) (conversation.Message, error) {
	var lists [4][]mailsync.Address
	for i, raw := range [][]byte{row.FromAddresses, row.ReplyToAddresses, row.ToAddresses, row.CcAddresses} {
		addresses, err := decodeAddresses(raw)
		if err != nil {
			return conversation.Message{}, err
		}
		lists[i] = addresses
	}
	mailboxes := make([]conversation.Mailbox, len(row.MailboxIds))
	for i, id := range row.MailboxIds {
		mailboxes[i] = conversation.Mailbox{
			ID:     id,
			Role:   mailsync.Role(row.MailboxRoles[i]),
			MadeBy: mailsync.Maker(row.MailboxMakers[i]),
		}
	}
	return conversation.Message{
		ID:        row.ID,
		Date:      row.InternalDate.UTC(),
		Subject:   row.Subject,
		Preview:   row.Preview,
		From:      lists[0],
		ReplyTo:   lists[1],
		To:        lists[2],
		CC:        lists[3],
		Seen:      row.Seen,
		Flagged:   row.Flagged,
		Mailboxes: mailboxes,
	}, nil
}
