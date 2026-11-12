package postgres

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/mailsync"
)

// threadMailboxes are a Gmail account's mailboxes with every role a
// conversation's rules read.
var threadMailboxes = []mailsync.Mailbox{
	{ProviderID: "CATEGORY_SOCIAL", Name: "CATEGORY_SOCIAL", MadeBy: mailsync.MadeByProvider},
	{ProviderID: "DRAFT", Name: "DRAFT", Role: mailsync.RoleDrafts, MadeBy: mailsync.MadeByProvider},
	{ProviderID: "INBOX", Name: "INBOX", Role: mailsync.RoleInbox, MadeBy: mailsync.MadeByProvider},
	{ProviderID: "Label_1", Name: "Receipts", MadeBy: mailsync.MadeByMember},
	{ProviderID: "SENT", Name: "SENT", Role: mailsync.RoleSent, MadeBy: mailsync.MadeByProvider},
	{ProviderID: "SPAM", Name: "SPAM", Role: mailsync.RoleJunk, MadeBy: mailsync.MadeByProvider},
	{ProviderID: "TRASH", Name: "TRASH", Role: mailsync.RoleTrash, MadeBy: mailsync.MadeByProvider},
}

// storedConversation is a conversation row as saved, with the provider IDs
// of the mailboxes it is in, sorted, and each one's copy of its date and
// unread.
type storedConversation struct {
	ID           uuid.UUID
	LatestDate   time.Time
	Subject      string
	Preview      string
	LatestFrom   []mailsync.Address
	Participants []mailsync.Address
	MessageCount int
	Unread       bool
	Flagged      bool
	InAllMail    bool
	Mailboxes    []string
	// Copies says whether every mailbox row repeats the conversation's
	// date and unread.
	Copies bool
}

// withThreadMailboxes gives the account [threadMailboxes].
func (s syncedMailTest) withThreadMailboxes(t *testing.T) syncedMailTest {
	t.Helper()
	s.update(t, s.account, mailsync.Change{State: running(1), Mailboxes: threadMailboxes})
	return s
}

// threadMessage returns a message of thread from sender, received day days
// after 1 November 2026, unread, in mailboxes.
func threadMessage(providerID, thread string, day int, sender string, mailboxes ...string) mailsync.Message {
	return mailsync.Message{
		ProviderID:         providerID,
		ThreadID:           thread,
		InternalDate:       time.Date(2026, 11, 1+day, 9, 0, 0, 0, time.UTC),
		Subject:            "Subject of " + providerID,
		From:               []mailsync.Address{{Name: sender, Address: sender + "@example.com"}},
		Preview:            "Preview of " + providerID,
		MailboxProviderIDs: mailboxes,
	}
}

// save writes messages to the account's synced mail.
func (s syncedMailTest) save(t *testing.T, messages ...mailsync.Message) {
	t.Helper()
	s.update(t, s.account, mailsync.Change{State: running(1), Saved: messages})
}

// conversations returns accountID's conversations by provider thread ID.
func (s syncedMailTest) conversations(t *testing.T, accountID int64) map[string]storedConversation {
	t.Helper()
	rows, err := s.conn.Query(t.Context(), `
		SELECT c.id, c.provider_thread_id, c.latest_date, c.subject, c.preview, c.latest_from, c.participants,
			c.message_count, c.unread, c.flagged, c.in_all_mail,
			array(SELECT mb.provider_mailbox_id FROM conversation_mailboxes cm JOIN mailboxes mb ON mb.id = cm.mailbox_id
				WHERE cm.conversation_id = c.id ORDER BY mb.provider_mailbox_id),
			NOT EXISTS (SELECT 1 FROM conversation_mailboxes cm WHERE cm.conversation_id = c.id
				AND (cm.latest_date <> c.latest_date OR cm.unread <> c.unread))
		FROM conversations c WHERE c.connected_account_id = $1`, accountID)
	require.NoError(t, err)
	defer rows.Close()

	conversations := map[string]storedConversation{}
	for rows.Next() {
		var c storedConversation
		var thread string
		var latestFrom, participants []byte
		require.NoError(t, rows.Scan(&c.ID, &thread, &c.LatestDate, &c.Subject, &c.Preview, &latestFrom, &participants,
			&c.MessageCount, &c.Unread, &c.Flagged, &c.InAllMail, &c.Mailboxes, &c.Copies))
		c.LatestDate = c.LatestDate.UTC()
		require.NoError(t, json.Unmarshal(latestFrom, &c.LatestFrom))
		require.NoError(t, json.Unmarshal(participants, &c.Participants))
		conversations[thread] = c
	}
	require.NoError(t, rows.Err())
	return conversations
}

func address(name string) mailsync.Address {
	return mailsync.Address{Name: name, Address: name + "@example.com"}
}

func TestConversationReply(t *testing.T) {
	// Given a received message in the inbox and Receipts, and my reply.
	s := newSyncedMailTest(t).withThreadMailboxes(t)
	received := threadMessage("m1", "t1", 1, "asha", "INBOX", "Label_1")
	received.Flagged = true
	reply := threadMessage("m2", "t1", 2, "member", "SENT")
	reply.Subject = "Re: Subject of m1"
	reply.Seen = true

	// When both are saved.
	s.save(t, received, reply)

	// Then one conversation, dated by the reply, shows the reply's preview and
	// sender, the first message's subject, both senders, and both places.
	got := s.conversations(t, s.account)
	require.Len(t, got, 1)
	c := got["t1"]
	assert.NotEqual(t, uuid.Nil(), c.ID)
	assert.Equal(t, reply.InternalDate, c.LatestDate)
	assert.Equal(t, "Subject of m1", c.Subject)
	assert.Equal(t, "Preview of m2", c.Preview)
	assert.Equal(t, []mailsync.Address{address("member")}, c.LatestFrom)
	assert.Equal(t, []mailsync.Address{address("asha"), address("member")}, c.Participants)
	assert.Equal(t, 2, c.MessageCount)
	assert.True(t, c.Unread, "the received message is unread")
	assert.True(t, c.Flagged)
	assert.True(t, c.InAllMail)
	assert.Equal(t, []string{"INBOX", "Label_1", "SENT"}, c.Mailboxes)
	assert.True(t, c.Copies)
	assert.Empty(t, s.conversations(t, s.other))
}

// Each write derives an existing row again in full, and the row keeps its
// ID while the thread has a message to show.
func TestConversationReDerives(t *testing.T) {
	// Given an unread inbox message, saved alone.
	s := newSyncedMailTest(t).withThreadMailboxes(t)
	original := threadMessage("m1", "t1", 1, "asha", "INBOX")
	reply := threadMessage("m2", "t1", 2, "member", "SENT")
	reply.Seen = true
	s.save(t, original)
	id := s.conversations(t, s.account)["t1"].ID
	alone := storedConversation{
		ID: id, LatestDate: original.InternalDate, Subject: "Subject of m1", Preview: "Preview of m1",
		LatestFrom: []mailsync.Address{address("asha")}, Participants: []mailsync.Address{address("asha")},
		MessageCount: 1, Unread: true, InAllMail: true, Mailboxes: []string{"INBOX"}, Copies: true,
	}
	require.Equal(t, alone, s.conversations(t, s.account)["t1"])

	// When a reply arrives in its own write, the newest message goes, and
	// the reply comes back without the original.
	s.save(t, reply)
	withReply := s.conversations(t, s.account)["t1"]
	s.update(t, s.account, mailsync.Change{State: running(1), Deleted: []string{"m2"}})
	newestGone := s.conversations(t, s.account)["t1"]
	s.save(t, reply)
	s.update(t, s.account, mailsync.Change{State: running(1), Deleted: []string{"m1"}})
	oldestGone := s.conversations(t, s.account)["t1"]

	// Then each write's row is the thread's as it is then, under one ID.
	assert.Equal(t, storedConversation{
		ID: id, LatestDate: reply.InternalDate, Subject: "Subject of m1", Preview: "Preview of m2",
		LatestFrom: []mailsync.Address{address("member")}, Participants: []mailsync.Address{address("asha"), address("member")},
		MessageCount: 2, Unread: true, InAllMail: true, Mailboxes: []string{"INBOX", "SENT"}, Copies: true,
	}, withReply)
	assert.Equal(t, alone, newestGone)
	assert.Equal(t, storedConversation{
		ID: id, LatestDate: reply.InternalDate, Subject: "Subject of m2", Preview: "Preview of m2",
		LatestFrom: []mailsync.Address{address("member")}, Participants: []mailsync.Address{address("member")},
		MessageCount: 1, InAllMail: true, Mailboxes: []string{"SENT"}, Copies: true,
	}, oldestGone)
}

func TestConversationTrashedReply(t *testing.T) {
	// Given an inbox message whose newer reply is in Trash.
	s := newSyncedMailTest(t).withThreadMailboxes(t)

	s.save(t, threadMessage("m1", "t1", 1, "asha", "INBOX"), threadMessage("m2", "t1", 2, "blair", "TRASH"))

	// Then the row is the inbox message's, and the conversation is in both
	// the inbox and Trash.
	c := s.conversations(t, s.account)["t1"]
	assert.Equal(t, "Preview of m1", c.Preview)
	assert.Equal(t, 1, c.MessageCount)
	assert.Equal(t, []mailsync.Address{address("asha")}, c.Participants)
	assert.True(t, c.InAllMail)
	assert.Equal(t, []string{"INBOX", "TRASH"}, c.Mailboxes)
}

func TestConversationOnlyHidden(t *testing.T) {
	// Given a conversation whose messages are all in Spam or Trash, one also
	// labelled.
	s := newSyncedMailTest(t).withThreadMailboxes(t)

	s.save(t, threadMessage("m1", "t1", 1, "asha", "SPAM", "Label_1"), threadMessage("m2", "t1", 2, "blair", "TRASH"))

	// Then its row is made from them, All mail and the label leave it out,
	// and Spam and Trash list it.
	c := s.conversations(t, s.account)["t1"]
	assert.Equal(t, "Preview of m2", c.Preview)
	assert.Equal(t, 2, c.MessageCount)
	assert.False(t, c.InAllMail)
	assert.Equal(t, []string{"SPAM", "TRASH"}, c.Mailboxes)
}

func TestConversationDrafts(t *testing.T) {
	// Given a conversation with a draft reply, and a conversation of a
	// draft alone.
	s := newSyncedMailTest(t).withThreadMailboxes(t)

	s.save(t,
		threadMessage("m1", "t1", 1, "asha", "INBOX"),
		threadMessage("draft-1", "t1", 2, "member", "DRAFT"),
		threadMessage("draft-2", "t2", 3, "member", "DRAFT"),
	)

	// Then drafts are left out of the one, and the other has no row.
	got := s.conversations(t, s.account)
	assert.Equal(t, []string{"t1"}, keys(got))
	assert.Equal(t, 1, got["t1"].MessageCount)
	assert.Equal(t, "Preview of m1", got["t1"].Preview)
	assert.Equal(t, []string{"INBOX"}, got["t1"].Mailboxes)
}

func TestConversationFlags(t *testing.T) {
	// Given an unread inbox conversation.
	s := newSyncedMailTest(t).withThreadMailboxes(t)
	s.save(t, threadMessage("m1", "t1", 1, "asha", "INBOX", "Label_1"))

	// When a poll reads it archived, read, and starred.
	s.update(t, s.account, mailsync.Change{State: running(1), Updated: []mailsync.MessageUpdate{
		{ProviderID: "m1", MailboxProviderIDs: []string{"Label_1"}, Seen: true, Flagged: true},
	}})

	// Then it leaves the inbox, keeps its label, and is read and starred.
	c := s.conversations(t, s.account)["t1"]
	assert.Equal(t, []string{"Label_1"}, c.Mailboxes)
	assert.False(t, c.Unread)
	assert.True(t, c.Flagged)
	assert.True(t, c.Copies)
}

func TestConversationDeleted(t *testing.T) {
	s := newSyncedMailTest(t).withThreadMailboxes(t)
	s.save(t, threadMessage("m1", "t1", 1, "asha", "INBOX"), threadMessage("m2", "t2", 2, "blair", "INBOX"))

	s.update(t, s.account, mailsync.Change{State: running(1), Deleted: []string{"m1"}})

	assert.Equal(t, []string{"t2"}, keys(s.conversations(t, s.account)), "a conversation goes with its last message")
	var memberships int
	require.NoError(t, s.conn.QueryRow(t.Context(), "SELECT count(*) FROM conversation_mailboxes").Scan(&memberships))
	assert.Equal(t, 1, memberships)
}

func TestConversationSwept(t *testing.T) {
	// Given generation 1 saved two conversations.
	s := newSyncedMailTest(t).withThreadMailboxes(t)
	s.save(t, threadMessage("m1", "t1", 1, "asha", "INBOX"), threadMessage("m2", "t2", 2, "blair", "INBOX"))

	// When generation 2 saves one and sweeps the rest.
	s.update(t, s.account, mailsync.Change{State: running(2), Saved: []mailsync.Message{threadMessage("m2", "t2", 2, "blair", "INBOX")}, DeleteEarlier: 10})

	assert.Equal(t, []string{"t2"}, keys(s.conversations(t, s.account)))
}

func TestConversationLabelDeleted(t *testing.T) {
	s := newSyncedMailTest(t).withThreadMailboxes(t)
	s.save(t, threadMessage("m1", "t1", 1, "asha", "INBOX", "Label_1"))
	withoutLabel := slices.DeleteFunc(slices.Clone(threadMailboxes), func(m mailsync.Mailbox) bool { return m.ProviderID == "Label_1" })

	s.update(t, s.account, mailsync.Change{State: running(1), Mailboxes: withoutLabel})

	assert.Equal(t, []string{"INBOX"}, s.conversations(t, s.account)["t1"].Mailboxes)
}

func TestConversationParticipants(t *testing.T) {
	// Given eleven senders, one writing twice under another name and case,
	// and a message whose From is a group with no address.
	s := newSyncedMailTest(t).withThreadMailboxes(t)
	messages := []mailsync.Message{threadMessage("m0", "t1", 0, "asha", "INBOX")}
	again := threadMessage("again", "t1", 1, "asha", "INBOX")
	again.From = []mailsync.Address{{Name: "Asha K", Address: "ASHA@example.com"}}
	group := threadMessage("group", "t1", 2, "asha", "INBOX")
	group.From = []mailsync.Address{{Name: "undisclosed-recipients:;"}}
	messages = append(messages, again, group)
	for i, sender := range []string{"b", "c", "d", "e", "f", "g", "h", "i", "j", "k"} {
		messages = append(messages, threadMessage("m-"+sender, "t1", 3+i, sender, "INBOX"))
	}

	s.save(t, messages...)

	// Then each sender appears once, as first written, in date order, and
	// only the first 10.
	got := s.conversations(t, s.account)["t1"].Participants
	assert.Equal(t, []mailsync.Address{
		address("asha"), {Name: "undisclosed-recipients:;"},
		address("b"), address("c"), address("d"), address("e"), address("f"), address("g"), address("h"), address("i"),
	}, got)
}

func TestConversationDisconnect(t *testing.T) {
	s := newSyncedMailTest(t).withThreadMailboxes(t)
	s.save(t, threadMessage("m1", "t1", 1, "asha", "INBOX"))

	_, err := s.conn.Exec(t.Context(), "DELETE FROM connected_accounts WHERE id = $1", s.account)
	require.NoError(t, err)

	var left int
	require.NoError(t, s.conn.QueryRow(t.Context(),
		"SELECT (SELECT count(*) FROM conversations) + (SELECT count(*) FROM conversation_mailboxes)").Scan(&left))
	assert.Equal(t, 0, left)
}
