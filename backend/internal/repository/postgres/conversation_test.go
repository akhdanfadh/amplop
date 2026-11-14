package postgres

import (
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/conversation"
	"inbok/internal/mailsync"
	"inbok/internal/messagecontent"
)

// conversationReadTest is a [syncedMailTest] whose two accounts have
// [threadMailboxes], and a stranger: another member with an account of
// their own.
type conversationReadTest struct {
	syncedMailTest
	stranger        int64
	strangerAccount int64
}

func newConversationReadTest(t *testing.T) conversationReadTest {
	t.Helper()
	s := newSyncedMailTest(t).withThreadMailboxes(t)
	s.update(t, s.other, mailsync.Change{State: running(1), Mailboxes: threadMailboxes})
	stranger := insertConnectingMember(t, s.conn, "stranger@example.com")
	strangerAccount := insertSyncingAccount(t, s.conn, stranger, "sub-stranger")
	s.update(t, strangerAccount, mailsync.Change{State: running(1), Mailboxes: threadMailboxes})
	return conversationReadTest{syncedMailTest: s, stranger: stranger, strangerAccount: strangerAccount}
}

// saveTo writes messages to accountID's synced mail.
func (c conversationReadTest) saveTo(t *testing.T, accountID int64, messages ...mailsync.Message) {
	t.Helper()
	c.update(t, accountID, mailsync.Change{State: running(1), Saved: messages})
}

// list returns the subjects of the member's page as query asks, failing
// the test on an error.
func (c conversationReadTest) list(t *testing.T, query conversation.Query) []string {
	t.Helper()
	if query.Limit == 0 {
		query.Limit = conversation.DefaultLimit
	}
	rows, err := c.db.ListConversations(t.Context(), c.member, query)
	require.NoError(t, err)
	subjects := make([]string, 0, len(rows))
	for _, row := range rows {
		subjects = append(subjects, row.Subject)
	}
	return subjects
}

var inbox = conversation.Place{View: conversation.ViewInbox}

func TestListAcrossAccounts(t *testing.T) {
	// Given inbox mail in both of the member's accounts, and the stranger's.
	c := newConversationReadTest(t)
	c.saveTo(t, c.account, threadMessage("a1", "t1", 1, "asha", "INBOX"), threadMessage("a3", "t3", 3, "asha", "INBOX"))
	c.saveTo(t, c.other, threadMessage("b2", "t2", 2, "blair", "INBOX"))
	c.saveTo(t, c.strangerAccount, threadMessage("s4", "t4", 4, "casey", "INBOX"))

	// When the inbox is listed for every account, then for one.
	every := c.list(t, conversation.Query{Place: inbox})
	one := c.list(t, conversation.Query{Place: inbox, Account: c.other})

	// Then every account's inbox merges newest first; one account's alone.
	assert.Equal(t, []string{"Subject of a3", "Subject of b2", "Subject of a1"}, every)
	assert.Equal(t, []string{"Subject of b2"}, one)
}

// A cursor with the zero ID holds only rows older than its date: no ID
// sorts below it, so none of that date's rows is left in.
func TestListOlderThanADate(t *testing.T) {
	// Given three inbox conversations of one date, over two accounts, and an
	// older and a newer one.
	c := newConversationReadTest(t)
	c.saveTo(t, c.account, threadMessage("a1", "t1", 1, "asha", "INBOX"), threadMessage("a3", "t3", 3, "asha", "INBOX"),
		threadMessage("a4", "t4", 3, "asha", "INBOX"), threadMessage("a5", "t5", 5, "asha", "INBOX"))
	c.saveTo(t, c.other, threadMessage("b3", "t6", 3, "blair", "INBOX"))
	whole, err := c.db.ListConversations(t.Context(), c.member, conversation.Query{Place: inbox, Limit: conversation.MaxLimit})
	require.NoError(t, err)
	var tied conversation.Cursor
	for _, row := range whole {
		if row.Subject == "Subject of a3" {
			tied.Date = row.LatestDate
		}
	}
	require.False(t, tied.Date.IsZero())

	// When the inbox is listed older than that date.
	older := c.list(t, conversation.Query{Place: inbox, Before: &tied})

	// Then it holds only the older one.
	assert.Equal(t, []string{"Subject of a1"}, older)
}

// Each view's pages, read after the last row of the one before, hold its
// whole list once, in date then ID order, across accounts and through
// conversations of one date that a page boundary splits.
func TestListViewPages(t *testing.T) {
	// Given seven starred inbox conversations over two accounts, three of
	// them on one date, and a stranger's.
	c := newConversationReadTest(t)
	starred := func(providerID string, day int, sender string) mailsync.Message {
		m := threadMessage(providerID, "t-"+providerID, day, sender, "INBOX")
		m.Flagged = true
		return m
	}
	c.saveTo(t, c.account, starred("a1", 1, "asha"), starred("a2", 2, "asha"), starred("a3", 3, "asha"),
		starred("a4", 3, "asha"), starred("a5", 4, "asha"))
	c.saveTo(t, c.other, starred("b3", 3, "blair"), starred("b5", 5, "blair"))
	c.saveTo(t, c.strangerAccount, starred("s3", 3, "casey"))

	for _, view := range []conversation.View{conversation.ViewInbox, conversation.ViewAll, conversation.ViewStarred} {
		t.Run(string(view), func(t *testing.T) {
			place := conversation.Place{View: view}
			whole, err := c.db.ListConversations(t.Context(), c.member, conversation.Query{Place: place, Limit: conversation.MaxLimit})
			require.NoError(t, err)

			// When it is read two at a time, each page after the last row.
			var paged []uuid.UUID
			query := conversation.Query{Place: place, Limit: 2}
			for range 5 {
				page, err := c.db.ListConversations(t.Context(), c.member, query)
				require.NoError(t, err)
				for _, row := range page {
					paged = append(paged, row.ID)
				}
				if len(page) == 0 {
					break
				}
				last := page[len(page)-1]
				query.Before = &conversation.Cursor{Date: last.LatestDate, ID: last.ID}
			}

			// Then the whole list is the member's seven, newest first, then by
			// ID, and the pages hold it once, in that order.
			require.Len(t, whole, 7)
			ids := make([]uuid.UUID, 0, len(whole))
			for i, row := range whole {
				ids = append(ids, row.ID)
				if i > 0 {
					previous := whole[i-1]
					assert.True(t, previous.LatestDate.After(row.LatestDate) ||
						(previous.LatestDate.Equal(row.LatestDate) && previous.ID.Compare(row.ID) > 0), "row %d is out of order", i)
				}
			}
			assert.Equal(t, ids, paged)
		})
	}
}

func TestListConversationsViews(t *testing.T) {
	// Given an inbox conversation, an archived starred one, a sent one,
	// one in Spam, and one in Trash.
	c := newConversationReadTest(t)
	starred := threadMessage("starred", "t2", 2, "blair")
	starred.Flagged = true
	trashedStar := threadMessage("trashed", "t5", 5, "erin", "TRASH")
	trashedStar.Flagged = true
	c.saveTo(t, c.account,
		threadMessage("inbox", "t1", 1, "asha", "INBOX"),
		starred,
		threadMessage("sent", "t3", 3, "member", "SENT"),
		threadMessage("spam", "t4", 4, "dana", "SPAM"),
		trashedStar,
	)

	listed := map[conversation.View][]string{}
	for _, view := range conversation.Views {
		listed[view] = c.list(t, conversation.Query{Place: conversation.Place{View: view}})
	}

	assert.Equal(t, map[conversation.View][]string{
		conversation.ViewInbox:   {"Subject of inbox"},
		conversation.ViewStarred: {"Subject of starred"},
		conversation.ViewSent:    {"Subject of sent"},
		conversation.ViewAll:     {"Subject of sent", "Subject of starred", "Subject of inbox"},
		conversation.ViewSpam:    {"Subject of spam"},
		conversation.ViewTrash:   {"Subject of trashed"},
	}, listed)
}

func TestListConversationsLabel(t *testing.T) {
	c := newConversationReadTest(t)
	c.saveTo(t, c.account, threadMessage("m1", "t1", 1, "asha", "Label_1"), threadMessage("m2", "t2", 2, "asha", "INBOX"))
	label := conversation.Place{Mailbox: c.mailboxID(t, c.account, "Label_1")}

	own := c.list(t, conversation.Query{Place: label})
	other := c.list(t, conversation.Query{Place: label, Account: c.other})

	assert.Equal(t, []string{"Subject of m1"}, own)
	assert.Empty(t, other, "another account has none of its labels")
}

// A place's mailbox must be one of the member's own labels, and an
// account filter one of the member's accounts; anything else is answered
// as missing.
func TestListConversationsRefused(t *testing.T) {
	c := newConversationReadTest(t)
	mailbox := func(accountID int64, providerID string) conversation.Place {
		return conversation.Place{Mailbox: c.mailboxID(t, accountID, providerID)}
	}
	cases := map[string]struct {
		query conversation.Query
		want  error
	}{
		"another member's label":   {conversation.Query{Place: mailbox(c.strangerAccount, "Label_1")}, conversation.ErrPlaceNotFound},
		"no such label":            {conversation.Query{Place: conversation.Place{Mailbox: uuid.New()}}, conversation.ErrPlaceNotFound},
		"gmail's own label":        {conversation.Query{Place: mailbox(c.account, "CATEGORY_SOCIAL")}, conversation.ErrPlaceNotFound},
		"the inbox by its ID":      {conversation.Query{Place: mailbox(c.account, "INBOX")}, conversation.ErrPlaceNotFound},
		"the drafts by their ID":   {conversation.Query{Place: mailbox(c.account, "DRAFT")}, conversation.ErrPlaceNotFound},
		"another member's account": {conversation.Query{Place: inbox, Account: c.strangerAccount}, conversation.ErrAccountNotFound},
	}
	for name, tc := range cases {
		t.Run(name+"/list", func(t *testing.T) {
			tc.query.Limit = conversation.DefaultLimit

			_, err := c.db.ListConversations(t.Context(), c.member, tc.query)

			require.ErrorIs(t, err, tc.want)
		})
		t.Run(name+"/count", func(t *testing.T) {
			_, err := c.db.CountConversationsAfter(t.Context(), c.member, tc.query.Place, tc.query.Account, time.Time{})

			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestListConversationsRow(t *testing.T) {
	// Given a labelled inbox message with a reply in Sent, also in
	// Gmail's own category.
	c := newConversationReadTest(t)
	received := threadMessage("m1", "t1", 1, "asha", "INBOX", "Label_1", "CATEGORY_SOCIAL")
	reply := threadMessage("m2", "t1", 2, "member", "SENT")
	reply.Seen = true
	c.saveTo(t, c.account, received, reply)

	rows, err := c.db.ListConversations(t.Context(), c.member, conversation.Query{Place: inbox, Limit: 1})

	require.NoError(t, err)
	require.Len(t, rows, 1)
	row := rows[0]
	assert.Equal(t, conversation.Conversation{
		ID:           row.ID,
		AccountID:    c.account,
		LatestDate:   reply.InternalDate,
		Subject:      "Subject of m1",
		Preview:      "Preview of m2",
		LatestFrom:   []mailsync.Address{address("member")},
		Participants: []mailsync.Address{address("asha"), address("member")},
		MessageCount: 2,
		Unread:       true,
		InInbox:      true,
		InSent:       true,
		InAllMail:    true,
		LabelIDs:     []uuid.UUID{c.mailboxID(t, c.account, "Label_1")},
	}, row)
}

func TestCountConversationsAfter(t *testing.T) {
	c := newConversationReadTest(t)
	c.saveTo(t, c.account, threadMessage("m1", "t1", 1, "asha", "INBOX"), threadMessage("m3", "t3", 3, "asha", "INBOX"))
	c.saveTo(t, c.other, threadMessage("m2", "t2", 2, "blair", "INBOX"))
	newest := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

	count, err := c.db.CountConversationsAfter(t.Context(), c.member, inbox, conversation.EveryAccount, newest)
	require.NoError(t, err)
	one, err := c.db.CountConversationsAfter(t.Context(), c.member, inbox, c.other, newest)
	require.NoError(t, err)

	assert.Equal(t, 2, count, "the two newer than the anchor, of both accounts")
	assert.Equal(t, 1, one)
}

func TestListPlaceCounts(t *testing.T) {
	// Given unread mail in the inbox, in a label, in Spam, and a starred
	// read one, all of the member's first account.
	c := newConversationReadTest(t)
	starred := threadMessage("m3", "t3", 3, "asha", "INBOX")
	starred.Seen, starred.Flagged = true, true
	c.saveTo(t, c.account,
		threadMessage("m1", "t1", 1, "asha", "INBOX", "Label_1"),
		threadMessage("m2", "t2", 2, "asha", "SPAM"),
		starred,
	)
	c.saveTo(t, c.strangerAccount, threadMessage("s1", "t1", 1, "asha", "INBOX"))

	counts, err := c.db.ListPlaceCounts(t.Context(), c.member)

	require.NoError(t, err)
	label := c.mailboxID(t, c.account, "Label_1")
	view := func(account int64, v conversation.View, unread int) conversation.PlaceCount {
		return conversation.PlaceCount{Account: account, Place: conversation.Place{View: v}, Unread: unread}
	}
	assert.Equal(t, []conversation.PlaceCount{
		view(c.account, conversation.ViewInbox, 1),
		view(c.account, conversation.ViewStarred, 0),
		view(c.account, conversation.ViewSent, 0),
		view(c.account, conversation.ViewAll, 1),
		view(c.account, conversation.ViewSpam, 1),
		view(c.account, conversation.ViewTrash, 0),
		{Account: c.account, Place: conversation.Place{Mailbox: label}, Name: "Receipts", Unread: 1},
		view(c.other, conversation.ViewInbox, 0),
		view(c.other, conversation.ViewStarred, 0),
		view(c.other, conversation.ViewSent, 0),
		view(c.other, conversation.ViewAll, 0),
		view(c.other, conversation.ViewSpam, 0),
		view(c.other, conversation.ViewTrash, 0),
		{Account: c.other, Place: conversation.Place{Mailbox: c.mailboxID(t, c.other, "Label_1")}, Name: "Receipts"},
	}, counts)
}

// A label gives its parent as one of the same account's own labels; a
// parent the account lacks, or one that is no label of the member's, is
// none.
func TestListPlaceCountsParent(t *testing.T) {
	c := newConversationReadTest(t)
	nested := append(slices.Clone(threadMailboxes),
		mailsync.Mailbox{ProviderID: "Label_2", Name: "2026", ProviderParentID: "Label_1", MadeBy: mailsync.MadeByMember},
		mailsync.Mailbox{ProviderID: "Label_3", Name: "Bills", ProviderParentID: "Label_gone", MadeBy: mailsync.MadeByMember},
		mailsync.Mailbox{ProviderID: "Label_4", Name: "Odd", ProviderParentID: "INBOX", MadeBy: mailsync.MadeByMember},
	)
	c.update(t, c.account, mailsync.Change{State: running(1), Mailboxes: nested})

	counts, err := c.db.ListPlaceCounts(t.Context(), c.member)

	require.NoError(t, err)
	parents := map[string]uuid.UUID{}
	for _, count := range counts {
		if count.Account == c.account && count.Place.View == "" {
			parents[count.Name] = count.Parent
		}
	}
	assert.Equal(t, map[string]uuid.UUID{
		"2026":     c.mailboxID(t, c.account, "Label_1"),
		"Bills":    uuid.Nil(),
		"Odd":      uuid.Nil(),
		"Receipts": uuid.Nil(),
	}, parents)
}

// A conversation in two mailboxes of one role counts once, as the list
// shows it once; an IMAP server may report one role on two folders.
func TestListPlaceCountsSameRole(t *testing.T) {
	c := newConversationReadTest(t)
	twoInboxes := append(slices.Clone(threadMailboxes), mailsync.Mailbox{ProviderID: "INBOX2", Name: "Inbox 2", Role: mailsync.RoleInbox, MadeBy: mailsync.MadeByProvider})
	c.update(t, c.account, mailsync.Change{State: running(1), Mailboxes: twoInboxes})
	c.saveTo(t, c.account, threadMessage("m1", "t1", 1, "asha", "INBOX", "INBOX2"))

	counts, err := c.db.ListPlaceCounts(t.Context(), c.member)

	require.NoError(t, err)
	assert.Equal(t, conversation.PlaceCount{Account: c.account, Place: inbox, Unread: 1}, counts[0])
}

func TestReadConversation(t *testing.T) {
	// Given a conversation of a labelled message with an attachment, a
	// trashed reply to it with an inline image, and a draft whose content
	// was never fetched.
	c := newConversationReadTest(t)
	first := threadMessage("m1", "t1", 1, "asha", "INBOX", "Label_1")
	first.To = []mailsync.Address{address("member")}
	first.MessageID = "<m1@example.com>"
	reply := threadMessage("m2", "t1", 2, "member", "TRASH")
	reply.MessageID = "<m2@example.com>"
	reply.InReplyTo = []string{"<m1@example.com>"}
	draft := threadMessage("draft", "t1", 3, "member", "DRAFT")
	c.saveTo(t, c.account, reply, first, draft)
	id := c.conversations(t, c.account)["t1"].ID
	stored := c.messages(t, c.account)
	_, err := c.db.SaveMessageParts(t.Context(), c.account, stored["m1"].ID, storedTree())
	require.NoError(t, err)
	_, err = c.db.SaveMessageParts(t.Context(), c.account, stored["m2"].ID, messagecontent.Part{
		ID: "", MediaType: "multipart/related", Kind: messagecontent.KindMultipart, Parts: []messagecontent.Part{
			{ID: "0", MediaType: "text/html", Kind: messagecontent.KindText, BlobID: uuid.New()},
			{ID: "1", MediaType: "image/png", Disposition: messagecontent.DispositionInline, Kind: messagecontent.KindInline},
		},
	})
	require.NoError(t, err)

	row, messages, err := c.db.ReadConversation(t.Context(), c.member, id)

	// Then every message comes, oldest first, each with its mailboxes by
	// name, its Message-ID and In-Reply-To, and whether it has an
	// attachment, which an inline image is not; the service leaves out the
	// draft.
	require.NoError(t, err)
	assert.Equal(t, id, row.ID)
	mailbox := func(providerID string, role mailsync.Role, maker mailsync.Maker) conversation.Mailbox {
		return conversation.Mailbox{ID: c.mailboxID(t, c.account, providerID), Role: role, MadeBy: maker}
	}
	none := []mailsync.Address{}
	assert.Equal(t, []conversation.Message{
		{
			ID: stored["m1"].ID, Date: first.InternalDate, Subject: "Subject of m1", Preview: "Preview of m1",
			From: []mailsync.Address{address("asha")}, ReplyTo: none, To: []mailsync.Address{address("member")}, CC: none,
			Mailboxes: []conversation.Mailbox{
				mailbox("INBOX", mailsync.RoleInbox, mailsync.MadeByProvider),
				mailbox("Label_1", mailsync.RoleNone, mailsync.MadeByMember),
			},
			MessageID: "<m1@example.com>", InReplyTo: []string{}, Attachments: true,
		},
		{
			ID: stored["m2"].ID, Date: reply.InternalDate, Subject: "Subject of m2", Preview: "Preview of m2",
			From: []mailsync.Address{address("member")}, ReplyTo: none, To: none, CC: none,
			Mailboxes: []conversation.Mailbox{mailbox("TRASH", mailsync.RoleTrash, mailsync.MadeByProvider)},
			MessageID: "<m2@example.com>", InReplyTo: []string{"<m1@example.com>"},
		},
		{
			ID: stored["draft"].ID, Date: draft.InternalDate, Subject: "Subject of draft", Preview: "Preview of draft",
			From: []mailsync.Address{address("member")}, ReplyTo: none, To: none, CC: none,
			Mailboxes: []conversation.Mailbox{mailbox("DRAFT", mailsync.RoleDrafts, mailsync.MadeByProvider)},
			InReplyTo: []string{},
		},
	}, messages)
}

// A message has attachments when its view offers a download, as
// messagecontent.Choose decides: an invite's calendar, an alternative the
// view leaves out, is none; an image outside multipart/related is one.
func TestReadConversationAttachments(t *testing.T) {
	c := newConversationReadTest(t)
	c.saveTo(t, c.account,
		threadMessage("invite", "t1", 1, "asha", "INBOX"),
		threadMessage("photo", "t1", 2, "asha", "INBOX"),
	)
	id := c.conversations(t, c.account)["t1"].ID
	stored := c.messages(t, c.account)
	text := func(partID, mediaType string) messagecontent.Part {
		return messagecontent.Part{ID: partID, MediaType: mediaType, Kind: messagecontent.KindText, BlobID: uuid.New()}
	}
	_, err := c.db.SaveMessageParts(t.Context(), c.account, stored["invite"].ID, messagecontent.Part{
		ID: "", MediaType: "multipart/alternative", Kind: messagecontent.KindMultipart, Parts: []messagecontent.Part{
			text("0", "text/plain"),
			text("1", "text/html"),
			{ID: "2", MediaType: "text/calendar", Kind: messagecontent.KindAttachment},
		},
	})
	require.NoError(t, err)
	_, err = c.db.SaveMessageParts(t.Context(), c.account, stored["photo"].ID, messagecontent.Part{
		ID: "", MediaType: "multipart/mixed", Kind: messagecontent.KindMultipart, Parts: []messagecontent.Part{
			text("0", "text/plain"),
			{ID: "1", MediaType: "image/png", ContentID: "photo", Disposition: messagecontent.DispositionInline, Kind: messagecontent.KindInline},
		},
	})
	require.NoError(t, err)

	_, messages, err := c.db.ReadConversation(t.Context(), c.member, id)

	require.NoError(t, err)
	require.Len(t, messages, 2)
	assert.False(t, messages[0].Attachments, "the invite's calendar is an alternative the view leaves out")
	assert.True(t, messages[1].Attachments, "an image no HTML shows is a download")
}

func TestReadConversationNotFound(t *testing.T) {
	c := newConversationReadTest(t)
	c.saveTo(t, c.strangerAccount, threadMessage("s1", "t1", 1, "asha", "INBOX"))
	strangers := c.conversations(t, c.strangerAccount)["t1"].ID

	for _, id := range []uuid.UUID{strangers, uuid.New()} {
		_, _, err := c.db.ReadConversation(t.Context(), c.member, id)

		require.ErrorIs(t, err, conversation.ErrNotFound)
	}
}
