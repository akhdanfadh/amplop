package google

import (
	"net/http"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/connectedaccount"
	"inbok/internal/conversation"
	"inbok/internal/googleemulator"
	"inbok/internal/mailsync"
)

// Each action changes every message of the thread, and the answer is
// where each stands after, as inbok keeps it, at a version past the one
// before: Trash by Gmail's own rules, which take the thread out of the
// inbox and back.
func TestChangeThread(t *testing.T) {
	client, token := startMailbox(t, nil, []googleemulator.Message{
		{ID: "m1", ThreadID: "t1", Mailbox: alexMailbox, From: "a@example.com", Subject: "Hi", LabelIDs: []string{labelInbox, labelUnread}},
		{ID: "m2", ThreadID: "t1", Mailbox: alexMailbox, From: "a@example.com", Subject: "Re: Hi", LabelIDs: []string{labelInbox}},
		{ID: "other", ThreadID: "t2", Mailbox: alexMailbox, From: "b@example.com", Subject: "Else", LabelIDs: []string{labelInbox, labelUnread}},
	})
	both := func(update mailsync.MessageUpdate) []mailsync.MessageUpdate {
		first, second := update, update
		first.ProviderID, second.ProviderID = "m1", "m2"
		return []mailsync.MessageUpdate{first, second}
	}
	inbox := []string{labelInbox}

	// In order, as each starts from where the one before left the thread.
	var last uint64
	for _, step := range []struct {
		action conversation.Action
		want   []mailsync.MessageUpdate
	}{
		{conversation.ActionRead, both(mailsync.MessageUpdate{MailboxProviderIDs: inbox, Seen: true})},
		{conversation.ActionUnread, both(mailsync.MessageUpdate{MailboxProviderIDs: inbox})},
		{conversation.ActionStar, both(mailsync.MessageUpdate{MailboxProviderIDs: inbox, Flagged: true})},
		{conversation.ActionUnstar, both(mailsync.MessageUpdate{MailboxProviderIDs: inbox})},
		{conversation.ActionArchive, both(mailsync.MessageUpdate{})},
		{conversation.ActionUnarchive, both(mailsync.MessageUpdate{MailboxProviderIDs: inbox})},
		{conversation.ActionSpam, both(mailsync.MessageUpdate{MailboxProviderIDs: []string{labelSpam}})},
		{conversation.ActionUnspam, both(mailsync.MessageUpdate{MailboxProviderIDs: inbox})},
		{conversation.ActionTrash, both(mailsync.MessageUpdate{MailboxProviderIDs: []string{labelTrash}})},
		{conversation.ActionUntrash, both(mailsync.MessageUpdate{MailboxProviderIDs: inbox})},
	} {
		updates, err := client.ChangeThread(t.Context(), token, "t1", step.action)

		require.NoError(t, err, string(step.action))
		changed, versions := versionless(updates)
		assert.ElementsMatch(t, step.want, changed, string(step.action))
		for _, version := range versions {
			assert.Greater(t, version, last, string(step.action))
		}
		last = slices.Max(versions)
	}

	// The other thread is untouched.
	fetched, err := client.Messages(t.Context(), token, 1, []string{"other"})
	require.NoError(t, err)
	require.Len(t, fetched.Messages, 1)
	assert.False(t, fetched.Messages[0].Seen)
	assert.Equal(t, inbox, fetched.Messages[0].MailboxProviderIDs)
}

// A thread Gmail no longer has is not found.
func TestChangeThreadNotFound(t *testing.T) {
	client, token := startMailbox(t, nil, nil)

	_, err := client.ChangeThread(t.Context(), token, "gone", conversation.ActionTrash)

	require.ErrorIs(t, err, conversation.ErrNotFound)
}

// The request asks for each message's labels only, and Gmail's answer
// without messages is an error, not a thread with none.
func TestChangeThreadNoMessages(t *testing.T) {
	client, _ := stubGmail(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/gmail/v1/users/me/threads/t1/modify", r.URL.Path)
		assert.Equal(t, threadFields, r.URL.Query().Get("fields"))
		gmailMessage(w, map[string]any{"id": "t1"})
	})

	_, err := client.ChangeThread(t.Context(), connectedaccount.NewAccessToken("access-1"), "t1", conversation.ActionRead)

	require.ErrorIs(t, err, errGmailAnswer)
}

// A thread Gmail keeps rate limiting is busy, after the retries.
func TestChangeThreadBusy(t *testing.T) {
	client, pauses := stubGmail(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	_, err := client.ChangeThread(t.Context(), connectedaccount.NewAccessToken("access-1"), "t1", conversation.ActionUntrash)

	require.ErrorIs(t, err, mailsync.ErrProviderBusy)
	assert.Len(t, pauses.all(), retryAttempts-1)
}
