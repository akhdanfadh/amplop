package postgres

import (
	"slices"
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/conversation"
	"inbok/internal/mailsync"
)

// [DB.ListThreads] finds the member's conversations' threads, in any of
// their accounts, and leaves out another member's and an unknown ID.
func TestListThreads(t *testing.T) {
	c := newConversationReadTest(t)
	c.saveTo(t, c.account, threadMessage("a1", "t1", 1, "asha", "INBOX"))
	c.saveTo(t, c.other, threadMessage("b1", "t2", 1, "blair", "INBOX"))
	c.saveTo(t, c.strangerAccount, threadMessage("s1", "t3", 1, "casey", "INBOX"))
	mine := c.conversations(t, c.account)["t1"].ID
	other := c.conversations(t, c.other)["t2"].ID
	strangers := c.conversations(t, c.strangerAccount)["t3"].ID

	threads, err := c.db.ListThreads(t.Context(), c.member, []uuid.UUID{mine, other, strangers, uuid.New()})

	require.NoError(t, err)
	assert.ElementsMatch(t, []conversation.Thread{
		{ID: mine, AccountID: c.account, ProviderThreadID: "t1"},
		{ID: other, AccountID: c.other, ProviderThreadID: "t2"},
	}, threads)
}

// Labels written for an action change the messages and their conversation,
// which keeps its ID and moves between places, and leave the sync state as
// it was.
func TestUpdateMessageLabels(t *testing.T) {
	// Given an unread inbox conversation of two messages, and one the
	// action leaves alone.
	c := newConversationReadTest(t)
	c.saveTo(t, c.account,
		threadMessage("m1", "t1", 1, "asha", "INBOX"),
		threadMessage("m2", "t1", 2, "blair", "INBOX"),
		threadMessage("m3", "t2", 3, "casey", "INBOX"))
	before := c.conversations(t, c.account)["t1"]
	require.True(t, before.Unread)
	state, err := c.db.SyncState(t.Context(), c.account)
	require.NoError(t, err)

	// When Gmail answers an archive and read with both messages' labels.
	err = c.db.UpdateMessageLabels(t.Context(), c.account, []mailsync.MessageUpdate{
		{ProviderID: "m1", MailboxProviderIDs: []string{"Label_1"}, Seen: true, Version: 2},
		{ProviderID: "m2", Seen: true, Flagged: true, Version: 2},
	})

	// Then the conversation is read, starred, out of the inbox, and in the
	// label; the other stays in the inbox.
	require.NoError(t, err)
	after := c.conversations(t, c.account)["t1"]
	assert.Equal(t, before.ID, after.ID)
	assert.False(t, after.Unread)
	assert.True(t, after.Flagged)
	assert.Equal(t, []string{"Label_1"}, after.Mailboxes)
	assert.Equal(t, []string{"Subject of m3"}, c.list(t, conversation.Query{Place: inbox}))
	kept, err := c.db.SyncState(t.Context(), c.account)
	require.NoError(t, err)
	assert.Equal(t, state, kept)
}

// A label Gmail answered with that inbok has not listed yet is left out
// and the rest of the change written; a poll at the same version, which
// lists the label first, then adds it back.
func TestUpdateLabelsNewMailbox(t *testing.T) {
	// Given a stored inbox message, and Gmail's answer naming a label
	// made since the last sync.
	c := newConversationReadTest(t)
	c.saveTo(t, c.account, threadMessage("m1", "t1", 1, "asha", "INBOX"))
	answer := []mailsync.MessageUpdate{{ProviderID: "m1", MailboxProviderIDs: []string{"INBOX", "Label_new"}, Seen: true, Version: 20}}

	// When the action writes it, then the poll that lists the label.
	err := c.db.UpdateMessageLabels(t.Context(), c.account, answer)
	require.NoError(t, err)
	written := c.messages(t, c.account)["m1"]
	c.update(t, c.account, mailsync.Change{State: running(1), Mailboxes: withNewLabel(), Updated: answer})

	// Then the action's write leaves the label out, and the poll adds it.
	assert.True(t, written.Seen)
	assert.Equal(t, []string{"INBOX"}, written.MailboxProviderIDs)
	assert.Equal(t, []string{"INBOX", "Label_new"}, c.messages(t, c.account)["m1"].MailboxProviderIDs)
}

// An action's answer written after a poll has listed its new label, at
// the same version, keeps that label.
func TestUpdateLabelsAfterPoll(t *testing.T) {
	c := newConversationReadTest(t)
	c.saveTo(t, c.account, threadMessage("m1", "t1", 1, "asha", "INBOX"))
	answer := []mailsync.MessageUpdate{{ProviderID: "m1", MailboxProviderIDs: []string{"INBOX", "Label_new"}, Seen: true, Version: 20}}
	c.update(t, c.account, mailsync.Change{State: running(1), Mailboxes: withNewLabel(), Updated: answer})

	err := c.db.UpdateMessageLabels(t.Context(), c.account, answer)

	require.NoError(t, err)
	assert.Equal(t, []string{"INBOX", "Label_new"}, c.messages(t, c.account)["m1"].MailboxProviderIDs)
}

// withNewLabel returns [threadMailboxes] and a label made since they were
// listed.
func withNewLabel() []mailsync.Mailbox {
	return append(slices.Clone(threadMailboxes), mailsync.Mailbox{ProviderID: "Label_new", Name: "New", MadeBy: mailsync.MadeByMember})
}

// An account without a sync state, e.g. one removed meanwhile, is not
// found.
func TestUpdateLabelsNoAccount(t *testing.T) {
	c := newConversationReadTest(t)

	err := c.db.UpdateMessageLabels(t.Context(), c.account+1000, []mailsync.MessageUpdate{{ProviderID: "m1", Version: 2}})

	require.ErrorIs(t, err, mailsync.ErrNotFound)
}

// [DB.ReadConversations] returns the member's rows in the order asked,
// leaving out another member's and an unknown ID.
func TestReadConversations(t *testing.T) {
	c := newConversationReadTest(t)
	c.saveTo(t, c.account, threadMessage("a1", "t1", 1, "asha", "INBOX"), threadMessage("a2", "t2", 2, "asha", "INBOX"))
	c.saveTo(t, c.strangerAccount, threadMessage("s1", "t3", 1, "casey", "INBOX"))
	first := c.conversations(t, c.account)["t1"].ID
	second := c.conversations(t, c.account)["t2"].ID
	strangers := c.conversations(t, c.strangerAccount)["t3"].ID

	rows, err := c.db.ReadConversations(t.Context(), c.member, []uuid.UUID{second, strangers, uuid.New(), first})

	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, second, rows[0].ID)
	assert.Equal(t, first, rows[1].ID)
	assert.True(t, rows[1].InInbox)
}
