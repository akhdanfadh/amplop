package postgres

import (
	"log/slog"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/gmail/v1"

	"inbok/internal/conversation"
	"inbok/internal/googleemulator"
	"inbok/internal/mailsync"
)

// An action on a synced account changes the thread at Gmail and inbok's
// rows to what Gmail answered, the poll that follows finds them already
// so, and its opposite undoes it in both.
func TestActionFlow(t *testing.T) {
	// Given alex's synced mailbox: an unread inbox thread of two messages,
	// and another thread.
	recent := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)
	mailbox := alexIdentity.Email
	f := newConnectFlowTest(t, nil,
		googleemulator.Message{ID: "m1", ThreadID: "t1", Mailbox: mailbox, From: "blair@example.com", Subject: "Plans",
			Received: recent, LabelIDs: []string{"INBOX", "UNREAD"}},
		googleemulator.Message{ID: "m2", ThreadID: "t1", Mailbox: mailbox, From: "casey@example.com", Subject: "Re: Plans",
			Received: recent.Add(time.Hour), LabelIDs: []string{"INBOX", "UNREAD"}},
		googleemulator.Message{ID: "m3", ThreadID: "t2", Mailbox: mailbox, From: "blair@example.com", Subject: "Other",
			Received: recent, LabelIDs: []string{"INBOX", "UNREAD"}},
	)
	account, err := f.consent(t, 0, mailbox)
	require.NoError(t, err)
	runFullSync(t, mailsync.NewFullSync(f.db, f.google, f.access, time.Now), account.ID)
	stored := syncedMailTest{db: f.db, conn: f.conn, account: account.ID}
	thread := stored.conversations(t, account.ID)["t1"].ID
	actions := conversation.NewActionService(f.db, f.access, f.google, slog.New(slog.DiscardHandler))
	var undo []conversation.Change
	apply := func(action conversation.Action) conversation.Conversation {
		t.Helper()
		applied, err := actions.Apply(t.Context(), f.member, action, []uuid.UUID{thread})
		require.NoError(t, err, string(action))
		require.Empty(t, applied.Refused, string(action))
		require.Len(t, applied.Conversations, 1, string(action))
		undo = applied.Undo
		return applied.Conversations[0]
	}
	// undoWith applies changes, an action's undo.
	undoWith := func(changes []conversation.Change) conversation.Conversation {
		t.Helper()
		applied, err := actions.ApplyChanges(t.Context(), f.member, changes)
		require.NoError(t, err)
		require.Empty(t, applied.Refused)
		require.Len(t, applied.Conversations, 1)
		return applied.Conversations[0]
	}

	// When the member archives the thread and marks it read.
	apply(conversation.ActionArchive)
	archived := undo
	row := apply(conversation.ActionRead)

	// Then the answer, inbok's messages, and Gmail agree: both messages
	// read and out of the inbox, the other thread untouched.
	assert.False(t, row.Unread)
	assert.False(t, row.InInbox)
	assert.True(t, row.InAllMail)
	messages := stored.messages(t, account.ID)
	for _, id := range []string{"m1", "m2"} {
		assert.True(t, messages[id].Seen, id)
		assert.Empty(t, messages[id].MailboxProviderIDs, id)
		at, err := f.gmailAt(t, account.ID).Users.Messages.Get("me", id).Format("minimal").Do()
		require.NoError(t, err)
		assert.Empty(t, at.LabelIds, id)
	}
	assert.False(t, messages["m3"].Seen)

	// And the poll that follows applies Gmail's record of the action:
	// the labels inbok already holds, now at the record's own version,
	// past the one the action wrote them at.
	runPoll(t, mailsync.NewPoll(f.db, f.google, f.access, time.Now), account.ID)
	polled := stored.messages(t, account.ID)
	for id, message := range messages {
		after := polled[id]
		if id != "m3" {
			assert.Greater(t, after.Version, message.Version, id)
		}
		after.Version = message.Version
		assert.Equal(t, message, after, id)
	}

	// When the member undoes the archive.
	require.Equal(t, []conversation.Change{{IDs: []uuid.UUID{thread}, Add: []conversation.Place{{View: conversation.ViewInbox}}}}, archived)
	row = undoWith(archived)

	// Then the thread is back in the inbox, still read.
	assert.True(t, row.InInbox)
	assert.False(t, row.Unread)
	assert.Equal(t, []string{"INBOX"}, stored.messages(t, account.ID)["m2"].MailboxProviderIDs)

	// When the member stars the thread and marks it unread.
	apply(conversation.ActionStar)
	row = apply(conversation.ActionUnread)

	// Then only the newest message is starred and unread, in inbok and at
	// Gmail, and the row shows both.
	assert.True(t, row.Flagged)
	assert.True(t, row.Unread)
	messages = stored.messages(t, account.ID)
	assert.True(t, messages["m1"].Seen)
	assert.False(t, messages["m1"].Flagged)
	assert.False(t, messages["m2"].Seen)
	assert.True(t, messages["m2"].Flagged)
	at, err := f.gmailAt(t, account.ID).Users.Messages.Get("me", "m1").Format("minimal").Do()
	require.NoError(t, err)
	assert.Equal(t, []string{"INBOX"}, at.LabelIds)

	// When the member reports it as spam.
	row = apply(conversation.ActionSpam)

	// Then it is only in Spam; not spam puts it back in the inbox.
	assert.True(t, row.InSpam)
	assert.False(t, row.InInbox)
	assert.False(t, row.InAllMail)
	row = apply(conversation.ActionUnspam)
	assert.False(t, row.InSpam)
	assert.True(t, row.InInbox)

	// When the member deletes it, then moves it out of Trash.
	row = apply(conversation.ActionTrash)

	// Then it is only in Trash, and out of Trash it is back in the inbox,
	// as Gmail's Move to Inbox puts it, though Trash took INBOX away.
	assert.True(t, row.InTrash)
	assert.False(t, row.InInbox)
	assert.False(t, row.InAllMail)
	row = apply(conversation.ActionUntrash)
	assert.False(t, row.InTrash)
	assert.True(t, row.InInbox)

	// When the member archives it, deletes it, and undoes the delete.
	apply(conversation.ActionArchive)
	apply(conversation.ActionTrash)
	row = undoWith(undo)

	// Then it is back where it was, archived, not in the inbox where Move
	// to Inbox would put it.
	assert.False(t, row.InTrash)
	assert.False(t, row.InInbox)
	assert.True(t, row.InAllMail)

	// When the member unstars it and undoes that.
	apply(conversation.ActionUnstar)
	row = undoWith(undo)

	// Then the newest message is starred again, alone.
	assert.True(t, row.Flagged)
	messages = stored.messages(t, account.ID)
	assert.False(t, messages["m1"].Flagged)
	assert.True(t, messages["m2"].Flagged)
}

// Undoing Delete on a thread with a message already in Trash brings the
// whole thread back to the inbox, rather than leave it in Trash, and
// leaves its unread mark alone, which only the row's reading changed.
func TestActionFlowPartlyTrashed(t *testing.T) {
	// Given alex's synced mailbox: a read inbox message and, in its
	// thread, an unread reply deleted on its own.
	recent := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)
	mailbox := alexIdentity.Email
	f := newConnectFlowTest(t, nil,
		googleemulator.Message{ID: "m1", ThreadID: "t1", Mailbox: mailbox, From: "blair@example.com", Subject: "Plans",
			Received: recent, LabelIDs: []string{"INBOX"}},
		googleemulator.Message{ID: "m2", ThreadID: "t1", Mailbox: mailbox, From: "casey@example.com", Subject: "Re: Plans",
			Received: recent.Add(time.Hour), LabelIDs: []string{"TRASH", "UNREAD"}},
	)
	account, err := f.consent(t, 0, mailbox)
	require.NoError(t, err)
	runFullSync(t, mailsync.NewFullSync(f.db, f.google, f.access, time.Now), account.ID)
	stored := syncedMailTest{db: f.db, conn: f.conn, account: account.ID}
	thread := stored.conversations(t, account.ID)["t1"].ID
	rows, err := f.db.ReadConversations(t.Context(), f.member, []uuid.UUID{thread})
	require.NoError(t, err)
	require.True(t, rows[0].InInbox)
	require.True(t, rows[0].InTrash)
	require.False(t, rows[0].Unread)
	actions := conversation.NewActionService(f.db, f.access, f.google, slog.New(slog.DiscardHandler))

	// When the member deletes it, its row now reading the deleted reply's
	// unread mark too, and undoes that.
	applied, err := actions.Apply(t.Context(), f.member, conversation.ActionTrash, []uuid.UUID{thread})
	require.NoError(t, err)
	require.Empty(t, applied.Refused)
	require.True(t, applied.Conversations[0].Unread)
	undone, err := actions.ApplyChanges(t.Context(), f.member, applied.Undo)
	require.NoError(t, err)
	require.Empty(t, undone.Refused)
	row := undone.Conversations[0]

	// Then it is back in the inbox and out of Trash, and the reply is
	// still unread, in inbok and at Gmail.
	assert.True(t, row.InInbox)
	assert.False(t, row.InTrash)
	assert.True(t, row.InAllMail)
	assert.False(t, stored.messages(t, account.ID)["m2"].Seen)
	at, err := f.gmailAt(t, account.ID).Users.Messages.Get("me", "m2").Format("minimal").Do()
	require.NoError(t, err)
	assert.Contains(t, at.LabelIds, "UNREAD")
	assert.NotContains(t, at.LabelIds, "TRASH")
}

// An action's answer written after a poll has brought in a newer change to
// the same messages leaves that change, so inbok still matches Gmail when
// no later poll will name the messages again.
func TestActionFlowLate(t *testing.T) {
	// Given alex's synced mailbox with one unread inbox thread.
	recent := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)
	mailbox := alexIdentity.Email
	f := newConnectFlowTest(t, nil,
		googleemulator.Message{ID: "m1", ThreadID: "t1", Mailbox: mailbox, From: "blair@example.com", Subject: "Plans",
			Received: recent, LabelIDs: []string{"INBOX", "UNREAD"}})
	account, err := f.consent(t, 0, mailbox)
	require.NoError(t, err)
	runFullSync(t, mailsync.NewFullSync(f.db, f.google, f.access, time.Now), account.ID)
	stored := syncedMailTest{db: f.db, conn: f.conn, account: account.ID}
	token, err := f.access.Token(t.Context(), account.ID)
	require.NoError(t, err)

	// When an action reads the thread at Gmail, the member then stars it
	// elsewhere, a poll brings the star in, and only then is the action's
	// answer written, at the version read before the call, as
	// conversation.ActionService writes it.
	version, err := f.google.Version(t.Context(), token)
	require.NoError(t, err)
	answer, err := f.google.ChangeThread(t.Context(), token, conversation.Thread{ProviderThreadID: "t1"}, conversation.ActionRead)
	require.NoError(t, err)
	for i := range answer {
		answer[i].Version = version
	}
	_, err = f.gmailAt(t, account.ID).Users.Messages.Modify("me", "m1", &gmail.ModifyMessageRequest{AddLabelIds: []string{"STARRED"}}).Do()
	require.NoError(t, err)
	runPoll(t, mailsync.NewPoll(f.db, f.google, f.access, time.Now), account.ID)
	require.NoError(t, f.db.UpdateMessages(t.Context(), account.ID, answer))

	// Then inbok keeps the newer state: read and starred, as Gmail has it.
	message := stored.messages(t, account.ID)["m1"]
	assert.True(t, message.Seen)
	assert.True(t, message.Flagged)
	assert.True(t, stored.conversations(t, account.ID)["t1"].Flagged)
}
