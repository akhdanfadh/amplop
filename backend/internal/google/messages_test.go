package google

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/gmail/v1"

	"inbok/internal/googleemulator"
	"inbok/internal/mailsync"
)

// testAccountID is the account the fetches are paced for.
const testAccountID int64 = 7

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

// Metadata comes back decoded, with Gmail's labels as mailboxes and flags;
// a message Gmail no longer has is gone.
func TestMessages(t *testing.T) {
	client, token := startMailbox(t, []googleemulator.Label{{ID: "Label_1", Mailbox: alexMailbox, Name: "Work"}},
		[]googleemulator.Message{{
			ID:         "m1",
			ThreadID:   "t1",
			Mailbox:    alexMailbox,
			From:       "=?UTF-8?B?w4lsaXNl?= <elise@example.com>",
			To:         `alex@example.com, "Chen, Blair" <blair@example.com>`,
			Cc:         "casey@example.com",
			ReplyTo:    "lists@example.com",
			Subject:    "=?UTF-8?Q?Caf=C3=A9_on_Friday?=",
			Snippet:    "It&#39;s at noon",
			LabelIDs:   []string{"INBOX", "UNREAD", "STARRED", "Label_1"},
			Received:   day(1),
			MessageID:  "<m1@example.com>",
			InReplyTo:  "<p2@example.com>",
			References: "<p1@example.com> <p2@example.com>",
		}, {
			ID: "m2", Mailbox: alexMailbox, From: "a@example.com", Subject: "Read", Received: day(0),
			LabelIDs: []string{"SENT"},
		}})

	fetched, err := client.Messages(t.Context(), token, testAccountID, []string{"m1", "missing", "m2"})

	require.NoError(t, err)
	assert.Equal(t, []string{"missing"}, fetched.Gone)
	require.Len(t, fetched.Messages, 2)
	assert.Equal(t, mailsync.Message{
		ProviderID:         "m1",
		ThreadID:           "t1",
		InternalDate:       day(1),
		Subject:            "Café on Friday",
		From:               []mailsync.Address{{Name: "Élise", Address: "elise@example.com"}},
		ReplyTo:            []mailsync.Address{{Address: "lists@example.com"}},
		To:                 []mailsync.Address{{Address: "alex@example.com"}, {Name: "Chen, Blair", Address: "blair@example.com"}},
		CC:                 []mailsync.Address{{Address: "casey@example.com"}},
		Preview:            "It's at noon",
		Seen:               false,
		Flagged:            true,
		MailboxProviderIDs: []string{"INBOX", "Label_1"},
		MessageID:          "<m1@example.com>",
		InReplyTo:          []string{"<p2@example.com>"},
		References:         []string{"<p1@example.com>", "<p2@example.com>"},
	}, fetched.Messages[0])
	read := fetched.Messages[1]
	assert.Equal(t, "m2", read.ProviderID)
	assert.True(t, read.Seen)
	assert.False(t, read.Flagged)
	assert.Equal(t, []string{"SENT"}, read.MailboxProviderIDs)
	assert.NotEmpty(t, read.MessageID, "the emulator makes one up, as a mail server would")
}

// gmailMessage writes m as Gmail's JSON.
func gmailMessage(w http.ResponseWriter, m map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(m)
}

// A message whose headers pass the answer limit is fetched again without
// them, so it still syncs.
func TestMessagesTooLarge(t *testing.T) {
	var mu sync.Mutex
	var formats []string
	client, _ := stubGmail(t, func(w http.ResponseWriter, r *http.Request) {
		format := r.URL.Query().Get("format")
		mu.Lock()
		formats = append(formats, format)
		mu.Unlock()
		message := map[string]any{"id": "m1", "threadId": "t1", "labelIds": []string{"INBOX"}, "snippet": "hi", "internalDate": "1762000000000"}
		if format == formatMetadata {
			message["payload"] = map[string]any{"headers": []map[string]string{
				{"name": "References", "value": strings.Repeat("<a@x> ", maxResponseBytes/6+1)},
			}}
		}
		gmailMessage(w, message)
	})

	fetched, err := client.Messages(t.Context(), stubToken, testAccountID, []string{"m1"})

	require.NoError(t, err)
	assert.Equal(t, []string{formatMetadata, formatMinimal}, formats)
	require.Len(t, fetched.Messages, 1)
	assert.Equal(t, "hi", fetched.Messages[0].Preview)
	assert.Nil(t, fetched.Messages[0].References)
}

// One fetch that fails fails the call, as its status only; a rate limit
// is retried first.
func TestMessagesFailure(t *testing.T) {
	var calls atomic.Int32
	client, p := stubGmail(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if strings.HasSuffix(r.URL.Path, "/m2") {
			refuse(w, http.StatusTooManyRequests, "rateLimitExceeded")
			return
		}
		gmailMessage(w, map[string]any{"id": strings.TrimPrefix(r.URL.Path, "/gmail/v1/users/me/messages/"), "threadId": "t"})
	})

	_, err := client.Messages(t.Context(), stubToken, testAccountID, []string{"m1", "m2", "m3"})

	require.ErrorIs(t, err, mailsync.ErrProviderBusy)
	assert.Contains(t, err.Error(), "429")
	assert.Len(t, p.all(), retryAttempts-1)
}

// A metadata answer with no value, JSON null, fails the fetch rather than
// the process.
func TestMessagesNoAnswer(t *testing.T) {
	client, _ := stubGmail(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("null"))
	})

	_, err := client.Messages(t.Context(), stubToken, testAccountID, []string{"m1"})

	require.ErrorIs(t, err, errGmailAnswer)
}

// A fetch whose wait for the account's pace would pass its deadline is
// refused at once as busy, sending nothing, while a context that ended
// returns its own error.
func TestMessagesPaceDeadline(t *testing.T) {
	var calls atomic.Int32
	client, _ := stubGmail(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		stubMessage(0)(w, r)
	})
	// Empty the bucket.
	_, err := client.Messages(t.Context(), stubToken, testAccountID, messageIDs(burstFetches))
	require.NoError(t, err)

	short, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err = client.Messages(short, stubToken, testAccountID, []string{"next"})
	require.ErrorIs(t, err, mailsync.ErrProviderBusy)
	require.NoError(t, short.Err(), "refused before the deadline")
	assert.Equal(t, int32(burstFetches), calls.Load(), "the refused fetch sent nothing")

	ended, stop := context.WithCancel(t.Context())
	stop()
	_, err = client.Messages(ended, stubToken, testAccountID, []string{"m3"})
	require.ErrorIs(t, err, context.Canceled)
}

// stubMessage answers messages.get with the message the path names, after
// hold.
func stubMessage(hold time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(hold)
		gmailMessage(w, map[string]any{"id": path.Base(r.URL.Path), "threadId": "t"})
	}
}

// messageIDs returns n message IDs.
func messageIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("m%d", i)
	}
	return ids
}

// perFetch is how long the pace makes each fetch after the first wait.
const perFetch = time.Minute * metadataUnits / fetchUnitsPerMinute

// burstFetches is how many metadata fetches a full pace lets go at once:
// its bucket holds a full fetch's units.
const burstFetches = fullUnits / metadataUnits

// An account's fetches are paced across calls, so many small calls go no
// faster than one large one; another account has its own pace.
func TestMessagesPace(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the pace")
	}
	client, _ := stubGmail(t, stubMessage(0))
	const calls = 8

	start := time.Now()
	for i := range calls {
		_, err := client.Messages(t.Context(), stubToken, testAccountID, []string{fmt.Sprintf("m%d", i)})
		require.NoError(t, err)
	}
	paced := time.Since(start)
	start = time.Now()
	_, err := client.Messages(t.Context(), stubToken, testAccountID+1, []string{"m0"})
	require.NoError(t, err)
	other := time.Since(start)

	// The first few go at once; each later one waits its units' share.
	assert.GreaterOrEqual(t, paced, perFetch*(calls-burstFetches)-perFetch/2)
	assert.Less(t, other, perFetch/2)
}

// At most four fetches of a call are in flight, however slow Gmail is.
func TestMessagesConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the pace")
	}
	// Each answer outlasts six fetches' pace, so without the bound six
	// would overlap.
	hold := 6 * perFetch
	var inFlight, most atomic.Int32
	slow := stubMessage(hold)
	client, _ := stubGmail(t, func(w http.ResponseWriter, r *http.Request) {
		now := inFlight.Add(1)
		defer inFlight.Add(-1)
		raiseMost(&most, now)
		slow(w, r)
	})

	fetched, err := client.Messages(t.Context(), stubToken, testAccountID, messageIDs(8))

	require.NoError(t, err)
	assert.Len(t, fetched.Messages, 8)
	assert.Equal(t, int32(fetchConcurrency), most.Load())
}

// raiseMost sets most to now if now is larger.
func raiseMost(most *atomic.Int32, now int32) {
	for {
		seen := most.Load()
		if now <= seen || most.CompareAndSwap(seen, now) {
			return
		}
	}
}

// its HTML escaping.
func TestMessageFromGmail(t *testing.T) {
	m := &gmail.Message{
		Id:           "m1",
		ThreadId:     "t1",
		LabelIds:     []string{"INBOX", "UNREAD", "STARRED", "Label_1"},
		Snippet:      "It&#39;s &quot;done&quot; &amp; sent\x00",
		InternalDate: 1762000000123,
		Payload: &gmail.MessagePart{Headers: []*gmail.MessagePartHeader{
			{Name: "Subject", Value: "=?UTF-8?Q?Caf=C3=A9?="},
			{Name: "From", Value: "Blair <blair@example.com>"},
			{Name: "Message-Id", Value: " <m1@example.com> "},
			{Name: "In-Reply-To", Value: "<p@example.com>"},
		}},
	}

	got := messageFromGmail(m)

	assert.Equal(t, "Café", got.Subject)
	assert.Equal(t, "It's \"done\" & sent\uFFFD", got.Preview)
	assert.False(t, got.Seen)
	assert.True(t, got.Flagged)
	assert.Equal(t, []string{"INBOX", "Label_1"}, got.MailboxProviderIDs)
	assert.Equal(t, "<m1@example.com>", got.MessageID)
	assert.Equal(t, []string{"<p@example.com>"}, got.InReplyTo)
	assert.Nil(t, got.References)
	assert.Equal(t, int64(1762000000123), got.InternalDate.UnixMilli())
	assert.Equal(t, []mailsync.Address{{Name: "Blair", Address: "blair@example.com"}}, got.From)
	assert.Nil(t, got.To)
}
