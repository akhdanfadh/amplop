package google

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/googleemulator"
	"inbok/internal/mailsync"
)

// day is a day before the emulator's clock, for received times.
func day(n int) time.Time {
	return time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC).AddDate(0, 0, n)
}

// The listing holds the messages received after Since, newest first, with
// spam and trash only when asked.
func TestListMessages(t *testing.T) {
	client, token := startMailbox(t, nil, []googleemulator.Message{
		{ID: "old", Mailbox: alexMailbox, From: "a@example.com", Subject: "Old", Received: day(-100)},
		{ID: "new", Mailbox: alexMailbox, From: "a@example.com", Subject: "New", Received: day(2)},
		{ID: "mid", Mailbox: alexMailbox, From: "a@example.com", Subject: "Mid", Received: day(1)},
		{ID: "spam", Mailbox: alexMailbox, From: "a@example.com", Subject: "Spam", Received: day(1), LabelIDs: []string{"SPAM"}},
	})
	since := day(-90)

	page, err := client.ListMessages(t.Context(), token, mailsync.ListQuery{Since: since})
	require.NoError(t, err)
	assert.Equal(t, []string{"new", "mid"}, page.ProviderIDs)
	assert.Empty(t, page.NextPageToken)

	page, err = client.ListMessages(t.Context(), token, mailsync.ListQuery{Since: since, IncludeJunkAndTrash: true})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"new", "mid", "spam"}, page.ProviderIDs)
}

// A listing longer than a page continues from its token, and pages never
// repeat a message.
func TestListMessagesPages(t *testing.T) {
	messages := make([]googleemulator.Message, listPageSize+3)
	for i := range messages {
		messages[i] = googleemulator.Message{
			ID: fmt.Sprintf("m%03d", i), Mailbox: alexMailbox, From: "a@example.com",
			Subject: "Hi", Received: day(0).Add(time.Duration(i) * time.Minute),
		}
	}
	client, token := startMailbox(t, nil, messages)
	query := mailsync.ListQuery{Since: day(-90)}

	first, err := client.ListMessages(t.Context(), token, query)
	require.NoError(t, err)
	require.Len(t, first.ProviderIDs, listPageSize)
	require.NotEmpty(t, first.NextPageToken)
	query.PageToken = first.NextPageToken
	second, err := client.ListMessages(t.Context(), token, query)
	require.NoError(t, err)

	assert.Equal(t, fmt.Sprintf("m%03d", listPageSize+2), first.ProviderIDs[0])
	assert.Equal(t, []string{"m002", "m001", "m000"}, second.ProviderIDs)
	assert.Empty(t, second.NextPageToken)
}
