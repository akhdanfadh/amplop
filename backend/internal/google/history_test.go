package google

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/gmail/v1"

	"amplop/internal/googleemulator"
	"amplop/internal/mailsync"
)

// Label changes after a position come back as updates with each
// message's whole state, UNREAD as the seen flag, at a version past the
// start, and the page moves the position on.
func TestChanges(t *testing.T) {
	client, token := startMailbox(t, nil, []googleemulator.Message{
		{ID: "m1", Mailbox: alexMailbox, From: "a@example.com", Subject: "Hi", LabelIDs: []string{"INBOX", "UNREAD"}},
		{ID: "m2", Mailbox: alexMailbox, From: "a@example.com", Subject: "Hi", LabelIDs: []string{"INBOX"}},
	})
	start, err := client.Position(t.Context(), token)
	require.NoError(t, err)
	service, err := client.gmail(t.Context(), token)
	require.NoError(t, err)
	_, err = service.Users.Messages.Modify(gmailUser, "m1", &gmail.ModifyMessageRequest{RemoveLabelIds: []string{"UNREAD"}}).Do()
	require.NoError(t, err)
	_, err = service.Users.Messages.Trash(gmailUser, "m2").Do()
	require.NoError(t, err)

	page, err := client.Changes(t.Context(), token, start, "")

	require.NoError(t, err)
	updates, versions := versionless(page.Update)
	assert.Equal(t, []mailsync.MessageUpdate{
		{ProviderID: "m1", MailboxProviderIDs: []string{labelInbox}, Seen: true},
		{ProviderID: "m2", MailboxProviderIDs: []string{labelTrash}, Seen: true},
	}, updates)
	first, err := strconv.ParseUint(start, 10, 64)
	require.NoError(t, err)
	for _, version := range versions {
		assert.Greater(t, version, first)
	}
	assert.Empty(t, page.Fetch)
	assert.Empty(t, page.Deleted)
	assert.NotEqual(t, start, page.Position)
	assert.Empty(t, page.NextPageToken)

	again, err := client.Changes(t.Context(), token, page.Position, "")
	require.NoError(t, err)
	assert.Empty(t, again.Update)
}

// versionless returns updates without their versions, and the versions.
func versionless(updates []mailsync.MessageUpdate) ([]mailsync.MessageUpdate, []uint64) {
	stripped := make([]mailsync.MessageUpdate, len(updates))
	versions := make([]uint64, len(updates))
	for i, update := range updates {
		versions[i] = update.Version
		update.Version = 0
		stripped[i] = update
	}
	return stripped, versions
}

// historyMessages returns history entries naming each of ids, their
// message carrying labelIDs when given.
func historyMessages(labelIDs []string, ids ...string) []map[string]any {
	entries := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		message := map[string]any{"id": id}
		if labelIDs != nil {
			message["labelIds"] = labelIDs
		}
		entries = append(entries, map[string]any{"message": message, "labelIds": []string{"ignored"}})
	}
	return entries
}

// A page is reduced per message: deleted wins, added is fetched whole, and
// a relabelled message takes the whole label set of its last label record,
// UNREAD and STARRED as flags, at that record's ID as its version, or is
// reread when that record carries none.
func TestChangesReduced(t *testing.T) {
	client, _ := stubGmail(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "7", r.URL.Query().Get("startHistoryId"))
		assert.Equal(t, "next", r.URL.Query().Get("pageToken"))
		// Neither this stub nor the emulator honours fields, so check each
		// label record's message labels are asked for.
		assert.Contains(t, r.URL.Query().Get("fields"), "labelsAdded/message(id,labelIds)")
		assert.Contains(t, r.URL.Query().Get("fields"), "labelsRemoved/message(id,labelIds)")
		assert.Contains(t, r.URL.Query().Get("fields"), "history(id,")
		gmailMessage(w, map[string]any{
			"historyId":     "24",
			"nextPageToken": "after",
			"history": []map[string]any{
				{"id": "8", "messagesAdded": historyMessages(nil, "a", "b")},
				{"id": "9", "labelsAdded": historyMessages([]string{labelInbox, labelStarred}, "a")},
				{"id": "10", "messagesDeleted": historyMessages(nil, "b", "c")},
				{"id": "11", "labelsRemoved": historyMessages([]string{"Label_1", labelUnread}, "d")},
				{"id": "12", "messagesDeleted": historyMessages(nil, "c")},
				{"id": "13", "labelsAdded": historyMessages([]string{labelInbox, "Label_1", labelUnread}, "d")},
				{"id": "14", "labelsRemoved": historyMessages([]string{labelInbox, labelUnread}, "d")},
				{"id": "15", "labelsAdded": historyMessages([]string{labelTrash, labelStarred}, "e")},
				{"id": "16", "labelsRemoved": historyMessages([]string{labelTrash}, "e")},
				{"id": "17", "labelsAdded": historyMessages(nil, "f")},
				{"id": "18", "labelsAdded": historyMessages([]string{labelInbox}, "g")},
				{"id": "19", "messagesDeleted": historyMessages(nil, "g")},
				{"id": "20", "labelsRemoved": historyMessages([]string{labelInbox, "Label_2"}, "h")},
				{"id": "21", "labelsAdded": historyMessages(nil, "h")},
				{"id": "22", "labelsAdded": historyMessages([]string{labelStarred}, "i")},
				{
					"id":            "23",
					"labelsAdded":   historyMessages([]string{labelInbox}, "j"),
					"labelsRemoved": historyMessages([]string{labelInbox}, "j"),
				},
				nil,
				{"id": "24", "messagesAdded": []any{nil, map[string]any{"message": nil}, map[string]any{"message": map[string]any{"id": ""}}}},
			},
		})
	})

	page, err := client.Changes(t.Context(), stubToken, "7", "next")

	require.NoError(t, err)
	assert.Equal(t, mailsync.ChangePage{
		Fetch:  []string{"a"},
		Reread: []string{"f", "h"},
		Update: []mailsync.MessageUpdate{
			{ProviderID: "d", MailboxProviderIDs: []string{labelInbox}, Version: 14},
			{ProviderID: "e", MailboxProviderIDs: []string{labelTrash}, Seen: true, Version: 16},
			{ProviderID: "i", Seen: true, Flagged: true, Version: 22},
			{ProviderID: "j", MailboxProviderIDs: []string{labelInbox}, Seen: true, Version: 23},
		},
		Deleted:       []string{"b", "c", "g"},
		Position:      "24",
		NextPageToken: "after",
	}, page)
}

// Gmail's 404 for a history ID it no longer keeps means a full sync.
func TestChangesExpired(t *testing.T) {
	client, _ := stubGmail(t, func(w http.ResponseWriter, _ *http.Request) {
		refuse(w, http.StatusNotFound, "notFound")
	})

	_, err := client.Changes(t.Context(), stubToken, "7", "")

	require.ErrorIs(t, err, mailsync.ErrPositionExpired)
	assert.Contains(t, err.Error(), "404")
}

// A position that is no history ID means a full sync too, without asking
// Gmail, and the error leaves the text out.
func TestChangesUnreadable(t *testing.T) {
	for _, position := range []string{"", "-1", "private-marker", "18446744073709551616"} {
		t.Run(position, func(t *testing.T) {
			client, _ := stubGmail(t, func(http.ResponseWriter, *http.Request) {
				t.Error("asked Gmail")
			})

			_, err := client.Changes(t.Context(), stubToken, position, "")

			require.ErrorIs(t, err, mailsync.ErrPositionExpired)
			assert.NotContains(t, err.Error(), "private-marker")
		})
	}
}
