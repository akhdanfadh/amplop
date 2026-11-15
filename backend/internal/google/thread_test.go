package google

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/connectedaccount"
	"inbok/internal/conversation"
	"inbok/internal/googleemulator"
	"inbok/internal/mailsync"
)

// t1 is the thread of [TestChangeThread], with m2 its newest message.
var t1 = conversation.Thread{ProviderThreadID: "t1", ProviderNewestMessageID: "m2"}

// Star and Mark unread change the newest message, every other action every
// message of the thread, and the answer is where each message it names
// stands after, as inbok keeps it, without a version, as Gmail answers:
// Trash by Gmail's own rules, which take the thread out of the inbox and
// back.
func TestChangeThread(t *testing.T) {
	client, token := startMailbox(t, nil, []googleemulator.Message{
		{ID: "m1", ThreadID: "t1", Mailbox: alexMailbox, From: "a@example.com", Subject: "Hi", LabelIDs: []string{labelInbox, labelUnread}},
		{ID: "m2", ThreadID: "t1", Mailbox: alexMailbox, From: "a@example.com", Subject: "Re: Hi", LabelIDs: []string{labelInbox}},
		{ID: "other", ThreadID: "t2", Mailbox: alexMailbox, From: "b@example.com", Subject: "Else", LabelIDs: []string{labelInbox, labelUnread}},
	})
	inbox := []string{labelInbox}

	// In order, as each starts from where the one before left the thread.
	for _, step := range []struct {
		action conversation.Action
		want   []mailsync.MessageUpdate
	}{
		{conversation.ActionRead, []mailsync.MessageUpdate{{ProviderID: "m1", MailboxProviderIDs: inbox, Seen: true}, {ProviderID: "m2", MailboxProviderIDs: inbox, Seen: true}}},
		{conversation.ActionUnread, []mailsync.MessageUpdate{{ProviderID: "m2", MailboxProviderIDs: inbox}}},
		{conversation.ActionStar, []mailsync.MessageUpdate{{ProviderID: "m2", MailboxProviderIDs: inbox, Flagged: true}}},
		{conversation.ActionUnstar, []mailsync.MessageUpdate{{ProviderID: "m1", MailboxProviderIDs: inbox, Seen: true}, {ProviderID: "m2", MailboxProviderIDs: inbox}}},
		{conversation.ActionArchive, []mailsync.MessageUpdate{{ProviderID: "m1", Seen: true}, {ProviderID: "m2"}}},
		{conversation.ActionUnarchive, []mailsync.MessageUpdate{{ProviderID: "m1", MailboxProviderIDs: inbox, Seen: true}, {ProviderID: "m2", MailboxProviderIDs: inbox}}},
		{conversation.ActionSpam, []mailsync.MessageUpdate{{ProviderID: "m1", MailboxProviderIDs: []string{labelSpam}, Seen: true}, {ProviderID: "m2", MailboxProviderIDs: []string{labelSpam}}}},
		{conversation.ActionUnspam, []mailsync.MessageUpdate{{ProviderID: "m1", MailboxProviderIDs: inbox, Seen: true}, {ProviderID: "m2", MailboxProviderIDs: inbox}}},
		{conversation.ActionTrash, []mailsync.MessageUpdate{{ProviderID: "m1", MailboxProviderIDs: []string{labelTrash}, Seen: true}, {ProviderID: "m2", MailboxProviderIDs: []string{labelTrash}}}},
		{conversation.ActionUntrash, []mailsync.MessageUpdate{{ProviderID: "m1", MailboxProviderIDs: inbox, Seen: true}, {ProviderID: "m2", MailboxProviderIDs: inbox}}},
	} {
		updates, err := client.ChangeThread(t.Context(), token, t1, step.action)

		require.NoError(t, err, string(step.action))
		assert.ElementsMatch(t, step.want, updates, string(step.action))
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

	for _, action := range []conversation.Action{conversation.ActionTrash, conversation.ActionStar} {
		_, err := client.ChangeThread(t.Context(), token, conversation.Thread{ProviderThreadID: "gone", ProviderNewestMessageID: "gone"}, action)

		require.ErrorIs(t, err, conversation.ErrNotFound, string(action))
	}
}

// An action on the newest message asks for that message's labels only.
func TestChangeThreadNewestRequest(t *testing.T) {
	client, _ := stubGmail(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/gmail/v1/users/me/messages/m2/modify", r.URL.Path)
		assert.Equal(t, messageFields, r.URL.Query().Get("fields"))
		gmailMessage(w, map[string]any{"id": "m2", "labelIds": []string{labelInbox, labelStarred}})
	})

	updates, err := client.ChangeThread(t.Context(), connectedaccount.NewAccessToken("access-1"), t1, conversation.ActionStar)

	require.NoError(t, err)
	assert.Equal(t, []mailsync.MessageUpdate{{ProviderID: "m2", MailboxProviderIDs: []string{labelInbox}, Seen: true, Flagged: true}}, updates)
}

// The request asks for each message's labels only, and Gmail's answer
// without messages is an error, not a thread with none.
func TestChangeThreadNoMessages(t *testing.T) {
	client, _ := stubGmail(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/gmail/v1/users/me/threads/t1/modify", r.URL.Path)
		assert.Equal(t, threadFields, r.URL.Query().Get("fields"))
		gmailMessage(w, map[string]any{"id": "t1"})
	})

	_, err := client.ChangeThread(t.Context(), connectedaccount.NewAccessToken("access-1"), t1, conversation.ActionRead)

	require.ErrorIs(t, err, errGmailAnswer)
}

// A thread Gmail keeps rate limiting is busy, after the retries.
func TestChangeThreadBusy(t *testing.T) {
	client, pauses := stubGmail(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	_, err := client.ChangeThread(t.Context(), connectedaccount.NewAccessToken("access-1"), t1, conversation.ActionUntrash)

	require.ErrorIs(t, err, mailsync.ErrProviderBusy)
	assert.Len(t, pauses.all(), retryAttempts-1)
}

// The account's version is its history ID, and a change after it carries
// a greater one.
func TestVersion(t *testing.T) {
	client, token := startMailbox(t, nil, []googleemulator.Message{
		{ID: "m1", ThreadID: "t1", Mailbox: alexMailbox, From: "a@example.com", Subject: "Hi", LabelIDs: []string{labelInbox, labelUnread}},
	})

	before, err := client.Version(t.Context(), token)
	require.NoError(t, err)
	_, err = client.ChangeThread(t.Context(), token, t1, conversation.ActionRead)
	require.NoError(t, err)
	fetched, err := client.Messages(t.Context(), token, 1, []string{"m1"})
	require.NoError(t, err)
	require.Len(t, fetched.Messages, 1)

	assert.Positive(t, before)
	assert.Greater(t, fetched.Messages[0].Version, before)
}
