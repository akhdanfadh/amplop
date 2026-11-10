package postgres

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/googleemulator"
	"inbok/internal/mailsync"
)

// runFullSync runs every step of accountID's full sync in order, as the
// workflow does.
func runFullSync(t *testing.T, s *mailsync.FullSync, accountID int64) {
	t.Helper()
	ctx := t.Context()
	run, err := s.Start(ctx, accountID)
	require.NoError(t, err)
	token := ""
	for page := 0; ; page++ {
		token, err = s.CopyPage(ctx, accountID, run, page, token)
		require.NoError(t, err)
		if token == "" {
			break
		}
	}
	for more := true; more; {
		more, err = s.DeleteEarlier(ctx, accountID, run)
		require.NoError(t, err)
	}
	require.NoError(t, s.Finish(ctx, accountID, run))
}

// An account connected through the emulator is copied whole by a full sync
// through the Gmail client: its window, Spam and Trash included, with its
// labels as mailboxes; and a second full sync keeps each message's ID.
func TestFullSyncFlow(t *testing.T) {
	// Given alex's mailbox at the emulator, with a message older than the
	// window.
	recent := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)
	mailbox := alexIdentity.Email
	f := newConnectFlowTest(t,
		[]googleemulator.Label{{ID: "Label_1", Mailbox: mailbox, Name: "Receipts"}},
		googleemulator.Message{ID: "inbox", Mailbox: mailbox, From: "blair@example.com", Subject: "Inbox", Received: recent},
		googleemulator.Message{ID: "receipt", Mailbox: mailbox, From: "shop@example.com", Subject: "Receipt", Received: recent, LabelIDs: []string{"Label_1"}},
		googleemulator.Message{ID: "spam", Mailbox: mailbox, From: "spam@example.com", Subject: "Spam", Received: recent, LabelIDs: []string{"SPAM"}},
		googleemulator.Message{ID: "trash", Mailbox: mailbox, From: "blair@example.com", Subject: "Trash", Received: recent, LabelIDs: []string{"TRASH"}},
		googleemulator.Message{ID: "old", Mailbox: mailbox, From: "blair@example.com", Subject: "Old", Received: time.Now().UTC().Add(-100 * 24 * time.Hour)},
	)
	account, err := f.consent(t, 0, mailbox)
	require.NoError(t, err)
	assert.Equal(t, []int64{account.ID}, f.fullSyncJobs(t), "connecting saves the full sync")
	syncs := mailsync.NewFullSync(f.db, f.google, f.access, time.Now)
	stored := syncedMailTest{db: f.db, conn: f.conn, account: account.ID}

	// When its full sync runs, and runs again.
	runFullSync(t, syncs, account.ID)
	first := stored.messages(t, account.ID)
	firstState := stored.state(t)
	runFullSync(t, syncs, account.ID)

	// Then the window's messages are kept with their labels, Spam and
	// Trash included, and the old one is not; the state is complete at a
	// position; and the second run keeps every message's ID.
	assert.Equal(t, []string{"inbox", "receipt", "spam", "trash"}, keys(first))
	assert.Equal(t, []string{"INBOX"}, first["inbox"].MailboxProviderIDs)
	assert.Equal(t, []string{"Label_1"}, first["receipt"].MailboxProviderIDs)
	assert.Equal(t, []string{"SPAM"}, first["spam"].MailboxProviderIDs)
	assert.Equal(t, mailsync.StatusComplete, firstState.Status)
	assert.NotEmpty(t, firstState.Position)
	assert.Equal(t, 1, firstState.Generation)
	assert.Equal(t, 4, firstState.MessagesSynced)
	assert.Contains(t, stored.mailboxes(t, account.ID), mailsync.Mailbox{ProviderID: "Label_1", Name: "Receipts"})
	second := stored.messages(t, account.ID)
	for id, message := range first {
		assert.Equal(t, message.ID, second[id].ID, "message %s keeps its ID", id)
	}
	assert.Equal(t, 2, stored.state(t).Generation)
}

// A full sync of a copy holding mail older than the window reaches back to
// it: a message Gmail still has comes back as Gmail holds it now, and one
// Gmail no longer has is deleted.
func TestSyncFlowOlder(t *testing.T) {
	// Given a copy holding the old message, which aged out of the window
	// with a state Gmail has since changed, and another that Gmail deleted
	// meanwhile.
	old := time.Now().UTC().Add(-100 * 24 * time.Hour).Truncate(time.Second)
	mailbox := alexIdentity.Email
	f := newConnectFlowTest(t, nil,
		googleemulator.Message{ID: "old", Mailbox: mailbox, From: "blair@example.com", Subject: "Old", Received: old},
	)
	account, err := f.consent(t, 0, mailbox)
	require.NoError(t, err)
	syncs := mailsync.NewFullSync(f.db, f.google, f.access, time.Now)
	runFullSync(t, syncs, account.ID)
	stored := syncedMailTest{db: f.db, conn: f.conn, account: account.ID}
	stale := func(providerID string) mailsync.Message {
		m := syncedMessage(providerID)
		m.InternalDate = old
		m.MailboxProviderIDs = nil
		return m
	}
	stored.update(t, account.ID, mailsync.Change{State: stored.state(t), Saved: []mailsync.Message{stale("old"), stale("deleted-at-gmail")}})

	// When the full sync runs again.
	runFullSync(t, syncs, account.ID)

	// Then the one Gmail still has is kept as Gmail holds it, and the other
	// is gone.
	after := stored.messages(t, account.ID)
	assert.Equal(t, []string{"old"}, keys(after))
	assert.Equal(t, "Old", after["old"].Subject)
	assert.Equal(t, []string{"INBOX"}, after["old"].MailboxProviderIDs)
}
