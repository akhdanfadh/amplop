package postgres

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"

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
	for {
		token, err = s.CopyPage(ctx, accountID, run, token)
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

// runPoll plans accountID's sync, which must be a poll, and runs every page
// of it, each read once and applied part by part, as the workflow does.
func runPoll(t *testing.T, p *mailsync.Poll, accountID int64) {
	t.Helper()
	plan, err := p.Plan(t.Context(), accountID)
	require.NoError(t, err)
	require.Equal(t, mailsync.KindPoll, plan.Kind)
	token := ""
	for {
		page, err := p.ReadChanges(t.Context(), accountID, plan.Position, token)
		require.NoError(t, err)
		for part, more := 0, true; more; part++ {
			more, err = p.ApplyChanges(t.Context(), accountID, mailsync.PollFrom{Scope: mailsync.PollComplete, Position: plan.Position}, token, page, part)
			require.NoError(t, err)
		}
		if page.NextPageToken == "" {
			return
		}
		token = page.NextPageToken
	}
}

// gmailAt returns a Gmail client for accountID at the emulator, as the
// member's own mail app would act on the account.
func (f connectFlowTest) gmailAt(t *testing.T, accountID int64) *gmail.Service {
	t.Helper()
	token, err := f.access.Token(t.Context(), accountID)
	require.NoError(t, err)
	authorized := &http.Client{Transport: &oauth2.Transport{
		Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token.Value()}),
	}}
	endpoint := f.emulator.BaseURL.Scheme + "://" + f.emulator.BaseURL.Host + "/"
	service, err := gmail.NewService(t.Context(), option.WithHTTPClient(authorized), option.WithEndpoint(endpoint))
	require.NoError(t, err)
	return service
}

// An account connected through the emulator is copied whole by a full sync
// through the Gmail client: its window, Spam and Trash included, with its
// labels as mailboxes; a second full sync keeps each message's ID; and the
// schedule then finds the account changed, and a poll applies the change.
func TestSyncFlow(t *testing.T) {
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
	assert.Equal(t, []int64{account.ID}, f.syncJobs(t), "connecting saves the sync")
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
	assert.Contains(t, stored.mailboxes(t, account.ID), mailsync.Mailbox{ProviderID: "Label_1", Name: "Receipts"})
	second := stored.messages(t, account.ID)
	for id, message := range first {
		assert.Equal(t, message.ID, second[id].ID, "message %s keeps its ID", id)
	}
	assert.Equal(t, 2, stored.state(t).Generation)

	// When the member archives one message and stars another in Gmail.
	service := f.gmailAt(t, account.ID)
	_, err = service.Users.Messages.Modify("me", "inbox", &gmail.ModifyMessageRequest{RemoveLabelIds: []string{"INBOX"}}).Do()
	require.NoError(t, err)
	_, err = service.Users.Messages.Modify("me", "receipt", &gmail.ModifyMessageRequest{AddLabelIds: []string{"STARRED"}}).Do()
	require.NoError(t, err)

	// Then the schedule finds the account due, and a poll applies both
	// changes and moves the position.
	due, err := mailsync.NewSchedule(f.db, f.google, f.access, time.Now).Due(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []int64{account.ID}, due)
	before := stored.state(t).Position
	runPoll(t, mailsync.NewPoll(f.db, f.google, f.access, time.Now), account.ID)
	polled := stored.messages(t, account.ID)
	assert.Empty(t, polled["inbox"].MailboxProviderIDs)
	assert.True(t, polled["receipt"].Flagged)
	assert.Equal(t, []string{"Label_1"}, polled["receipt"].MailboxProviderIDs)
	assert.Equal(t, first["inbox"].ID, polled["inbox"].ID)
	after := stored.state(t)
	assert.NotEqual(t, before, after.Position)
	assert.Equal(t, mailsync.StatusComplete, after.Status)

	// And the schedule finds nothing more to do.
	due, err = mailsync.NewSchedule(f.db, f.google, f.access, time.Now).Due(t.Context())
	require.NoError(t, err)
	assert.Empty(t, due)
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

// A poll inside a running full sync applies what changed since its start,
// against PostgreSQL and the emulator: the copy goes on, the sweep keeps
// what the poll wrote, and the finish leaves the position the poll moved.
func TestSyncFlowPollDuringCopy(t *testing.T) {
	// Given a full sync started and its page copied.
	recent := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)
	mailbox := alexIdentity.Email
	f := newConnectFlowTest(t, nil,
		googleemulator.Message{ID: "inbox", Mailbox: mailbox, From: "blair@example.com", Subject: "Inbox", Received: recent},
	)
	account, err := f.consent(t, 0, mailbox)
	require.NoError(t, err)
	syncs := mailsync.NewFullSync(f.db, f.google, f.access, time.Now)
	run, err := syncs.Start(t.Context(), account.ID)
	require.NoError(t, err)
	_, err = syncs.CopyPage(t.Context(), account.ID, run, "")
	require.NoError(t, err)

	// When the member archives the message, and the run polls before its
	// sweep and finish. (The emulator deletes a message only with the full
	// mail scope inbok does not ask for, so deletions are covered in
	// mailsync's TestFullSyncPollDeletes.)
	_, err = f.gmailAt(t, account.ID).Users.Messages.Modify("me", "inbox", &gmail.ModifyMessageRequest{RemoveLabelIds: []string{"INBOX"}}).Do()
	require.NoError(t, err)
	polls := mailsync.NewPoll(f.db, f.google, f.access, time.Now)
	from := mailsync.PollFrom{Scope: mailsync.PollDuringCopy, Position: run.Position, Generation: run.Generation}
	page, err := polls.ReadChanges(t.Context(), account.ID, from.Position, "")
	require.NoError(t, err)
	for part, more := 0, true; more; part++ {
		more, err = polls.ApplyChanges(t.Context(), account.ID, from, "", page, part)
		require.NoError(t, err)
	}
	for more := true; more; {
		more, err = syncs.DeleteEarlier(t.Context(), account.ID, run)
		require.NoError(t, err)
	}
	require.NoError(t, syncs.Finish(t.Context(), account.ID, run))

	// Then the message is archived, and the state complete at the
	// position the poll moved to.
	stored := syncedMailTest{db: f.db, conn: f.conn, account: account.ID}
	after := stored.messages(t, account.ID)
	assert.Equal(t, []string{"inbox"}, keys(after))
	assert.Empty(t, after["inbox"].MailboxProviderIDs)
	state := stored.state(t)
	assert.Equal(t, mailsync.StatusComplete, state.Status)
	assert.Equal(t, page.Position, state.Position)
	assert.NotEqual(t, run.Position, state.Position)
}
