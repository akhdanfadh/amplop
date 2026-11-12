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
	apply := func(action conversation.Action) conversation.Conversation {
		t.Helper()
		applied, err := actions.Apply(t.Context(), f.member, action, []uuid.UUID{thread})
		require.NoError(t, err, string(action))
		require.Empty(t, applied.Refused, string(action))
		require.Len(t, applied.Conversations, 1, string(action))
		return applied.Conversations[0]
	}

	// When the member archives the thread and marks it read.
	apply(conversation.ActionArchive)
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

	// And the poll that follows applies Gmail's record of the action,
	// which inbok already holds.
	runPoll(t, mailsync.NewPoll(f.db, f.google, f.access, time.Now), account.ID)
	assert.Equal(t, messages, stored.messages(t, account.ID))

	// When the member undoes the archive.
	row = apply(conversation.ActionArchive.Opposite())

	// Then the thread is back in the inbox, still read.
	assert.True(t, row.InInbox)
	assert.False(t, row.Unread)
	assert.Equal(t, []string{"INBOX"}, stored.messages(t, account.ID)["m2"].MailboxProviderIDs)
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
	// answer written.
	answer, err := f.google.ChangeThread(t.Context(), token, "t1", conversation.ActionRead)
	require.NoError(t, err)
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
