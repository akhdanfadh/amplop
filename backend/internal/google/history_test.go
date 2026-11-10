package google

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/gmail/v1"

	"inbok/internal/googleemulator"
	"inbok/internal/mailsync"
)

// A label change after a history ID is a change since it, and the page
// moves the history ID on.
func TestChanges(t *testing.T) {
	client, token := startMailbox(t, nil, []googleemulator.Message{
		{ID: "m1", Mailbox: alexMailbox, From: "a@example.com", Subject: "Hi", LabelIDs: []string{"INBOX", "UNREAD"}},
		{ID: "m2", Mailbox: alexMailbox, From: "a@example.com", Subject: "Hi", LabelIDs: []string{"INBOX"}},
	})
	start, err := client.HistoryID(t.Context(), token)
	require.NoError(t, err)
	service, err := client.gmail(t.Context(), token)
	require.NoError(t, err)
	_, err = service.Users.Messages.Modify("me", "m1", &gmail.ModifyMessageRequest{RemoveLabelIds: []string{"UNREAD"}}).Do()
	require.NoError(t, err)
	_, err = service.Users.Messages.Trash("me", "m2").Do()
	require.NoError(t, err)

	page, err := client.Changes(t.Context(), token, start, "")

	require.NoError(t, err)
	assert.Equal(t, []string{"m1", "m2"}, page.Changed)
	assert.Empty(t, page.Deleted)
	assert.Greater(t, page.HistoryID, start)
	assert.Empty(t, page.NextPageToken)

	again, err := client.Changes(t.Context(), token, page.HistoryID, "")
	require.NoError(t, err)
	assert.Empty(t, again.Changed)
}

// A message deleted is only deleted, whatever else happened to it in the
// page, and each ID appears once.
func TestChangesDeleted(t *testing.T) {
	client, _ := stubGmail(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "7", r.URL.Query().Get("startHistoryId"))
		assert.Equal(t, "next", r.URL.Query().Get("pageToken"))
		gmailMessage(w, map[string]any{
			"historyId":     "12",
			"nextPageToken": "after",
			"history": []map[string]any{
				{"id": "8", "messagesAdded": []map[string]any{{"message": map[string]any{"id": "a"}}, {"message": map[string]any{"id": "b"}}}},
				{"id": "9", "labelsAdded": []map[string]any{{"message": map[string]any{"id": "a"}, "labelIds": []string{"STARRED"}}}},
				{"id": "10", "messagesDeleted": []map[string]any{{"message": map[string]any{"id": "b"}}, {"message": map[string]any{"id": "c"}}}},
				{"id": "11", "labelsRemoved": []map[string]any{{"message": map[string]any{"id": "d"}, "labelIds": []string{"INBOX"}}}},
				{"id": "12", "messagesDeleted": []map[string]any{{"message": map[string]any{"id": "c"}}}},
			},
		})
	})

	page, err := client.Changes(t.Context(), stubToken, 7, "next")

	require.NoError(t, err)
	assert.Equal(t, mailsync.ChangePage{
		Changed:       []string{"a", "d"},
		Deleted:       []string{"b", "c"},
		HistoryID:     12,
		NextPageToken: "after",
	}, page)
}

// Gmail's 404 for a history ID it no longer keeps means a full sync.
func TestChangesExpired(t *testing.T) {
	client, _ := stubGmail(t, func(w http.ResponseWriter, _ *http.Request) {
		refuse(w, http.StatusNotFound, "notFound")
	})

	_, err := client.Changes(t.Context(), stubToken, 7, "")

	require.ErrorIs(t, err, mailsync.ErrHistoryExpired)
	assert.Contains(t, err.Error(), "404")
}
