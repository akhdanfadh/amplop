package google

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/connectedaccount"
	"inbok/internal/outgoingmessage"
)

// rawMessage returns a message from alex@example.com carrying inbokID as
// its X-Inbok-Message-Id and Message-ID.
func rawMessage(inbokID string) []byte {
	return []byte("From: alex@example.com\r\nTo: blair@example.com\r\nSubject: Lunch\r\n" +
		"Message-Id: " + inbokID + "\r\nX-Inbok-Message-Id: " + inbokID + "\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\nFriday?\r\n")
}

// stubGmail returns a client whose Gmail is handler.
func stubGmail(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	return New(Settings{ClientID: testClientID, ClientSecret: testSecret, RedirectURL: testRedirect, BaseURL: base}, time.Now)
}

// gmailError writes Gmail's error body with status, message, and reason.
func gmailError(w http.ResponseWriter, status int, message, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"error":{"code":%d,"message":%q,"errors":[{"message":%q,"domain":"global","reason":%q}]}}`,
		status, message, message, reason)
}

// writeJSON writes value as a 200 JSON answer.
func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

// The emulator sends the message into Sent, and the check finds it there
// by its X-Inbok-Message-Id from the position read before.
func TestSendAndFind(t *testing.T) {
	e, client := startEmulator(t)
	code, verifier := consent(t, e, client, "alex@example.com")
	grant, err := client.Exchange(t.Context(), code, verifier)
	require.NoError(t, err)
	token := grant.AccessToken

	mailbox, err := client.Mailbox(t.Context(), token)
	require.NoError(t, err)
	assert.Equal(t, "alex@example.com", mailbox.Email)
	assert.NotEmpty(t, mailbox.HistoryID)

	sent, err := client.Send(t.Context(), token, rawMessage("<send-1@example.com>"))
	require.NoError(t, err)
	assert.NotEmpty(t, sent.ProviderMessageID)
	assert.NotEmpty(t, sent.ProviderThreadID)

	found, err := client.FindSent(t.Context(), token, mailbox.HistoryID, "<send-1@example.com>")
	require.NoError(t, err)
	assert.Equal(t, sent, found)

	_, err = client.FindSent(t.Context(), token, mailbox.HistoryID, "<other@example.com>")
	require.ErrorIs(t, err, outgoingmessage.ErrSentNotFound)
}

// Each of Gmail's answers is sorted by what it says of the message
// (Gmail's "Resolve errors" guide).
func TestSendAnswers(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		message string
		reason  string
		want    error
	}{
		{"invalid", http.StatusBadRequest, "Invalid To header", "invalidArgument", outgoingmessage.ErrRefused},
		{"domain policy", http.StatusForbidden, "Gmail apps disabled", "domainPolicy", outgoingmessage.ErrRefused},
		{"sending limit", http.StatusTooManyRequests, "User-rate limit exceeded (Mail sending)", "rateLimitExceeded", outgoingmessage.ErrSendingLimit},
		{"concurrent requests", http.StatusTooManyRequests, "Too many concurrent requests for user", "rateLimitExceeded", outgoingmessage.ErrNotSent},
		{"user rate", http.StatusForbidden, "User Rate Limit Exceeded", "userRateLimitExceeded", outgoingmessage.ErrNotSent},
		{"project daily", http.StatusForbidden, "Daily Limit Exceeded", "dailyLimitExceeded", outgoingmessage.ErrNotSent},
		{"token", http.StatusUnauthorized, "Invalid Credentials", "authError", outgoingmessage.ErrNotSent},
		{"server error", http.StatusInternalServerError, "Backend Error", "backendError", outgoingmessage.ErrUnanswered},
		{"unavailable", http.StatusServiceUnavailable, "Service unavailable", "backendError", outgoingmessage.ErrUnanswered},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client := stubGmail(t, func(w http.ResponseWriter, _ *http.Request) {
				gmailError(w, c.status, c.message, c.reason)
			})

			_, err := client.Send(t.Context(), connectedaccount.NewAccessToken("access-1"), rawMessage("<a@b>"))

			require.ErrorIs(t, err, c.want)
			assert.NotContains(t, err.Error(), c.message, "Gmail's text is not kept")
		})
	}
}

// The request goes as messages.send expects: raw, base64url.
func TestSendRequest(t *testing.T) {
	var body map[string]string
	client := stubGmail(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/gmail/v1/users/me/messages/send", r.URL.Path)
		assert.Equal(t, "Bearer access-1", r.Header.Get("Authorization"))
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		writeJSON(w, map[string]any{"id": "gm-1", "threadId": "th-1", "labelIds": []string{"SENT"}})
	})

	sent, err := client.Send(t.Context(), connectedaccount.NewAccessToken("access-1"), rawMessage("<a@b>"))

	require.NoError(t, err)
	assert.Equal(t, outgoingmessage.Sent{ProviderMessageID: "gm-1", ProviderThreadID: "th-1"}, sent)
	assert.NotContains(t, body["raw"], "+", "base64url")
	assert.NotContains(t, body["raw"], "/", "base64url")
}

// A connection dropped after the whole request went may have sent it.
func TestSendDropped(t *testing.T) {
	client := stubGmail(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		conn, _, err := w.(http.Hijacker).Hijack()
		if assert.NoError(t, err) {
			_ = conn.Close()
		}
	})

	_, err := client.Send(t.Context(), connectedaccount.NewAccessToken("access-1"), rawMessage("<a@b>"))

	require.ErrorIs(t, err, outgoingmessage.ErrUnanswered)
}

// An answer that times out after the whole request went may have sent it.
func TestSendTimeout(t *testing.T) {
	release := make(chan struct{})
	client := stubGmail(t, func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-release
	})
	t.Cleanup(func() { close(release) })
	client.http.Timeout = 200 * time.Millisecond

	_, err := client.Send(t.Context(), connectedaccount.NewAccessToken("access-1"), rawMessage("<a@b>"))

	require.ErrorIs(t, err, outgoingmessage.ErrUnanswered)
}

// Once connected, even a request whose write never finished may have
// reached Gmail: Go reports a finished write after the fact, e.g. over
// HTTP/2. Only no connection at all proves a message was not sent.
func TestSendWriteUnfinished(t *testing.T) {
	release := make(chan struct{})
	client := stubGmail(t, func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadFull(r.Body, make([]byte, 10))
		<-release
	})
	t.Cleanup(func() { close(release) })
	client.http.Timeout = 200 * time.Millisecond
	large := append(rawMessage("<a@b>"), []byte(strings.Repeat("x", 4<<20))...)

	_, err := client.Send(t.Context(), connectedaccount.NewAccessToken("access-1"), large)

	require.ErrorIs(t, err, outgoingmessage.ErrUnanswered)
}

// A 200 without Gmail's ID took the message but cannot name it, so the
// check finds it; it is never retried.
func TestSendNoID(t *testing.T) {
	client := stubGmail(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"threadId": "th-1"})
	})

	_, err := client.Send(t.Context(), connectedaccount.NewAccessToken("access-1"), rawMessage("<a@b>"))

	require.ErrorIs(t, err, outgoingmessage.ErrUnanswered)
}

// An answer that cannot be decoded keeps none of its text.
func TestSendUnreadableAnswer(t *testing.T) {
	client := stubGmail(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": "gm-1", "historyId": "secret page body"}`))
	})

	_, err := client.Send(t.Context(), connectedaccount.NewAccessToken("access-1"), rawMessage("<a@b>"))

	require.ErrorIs(t, err, outgoingmessage.ErrUnanswered)
	assert.NotContains(t, err.Error(), "secret page body")
}

// net/http reports an answer it cannot parse as a transport error quoting
// it, e.g. a malformed Content-Length; none of it is kept.
func TestMailboxMalformedAnswer(t *testing.T) {
	client := stubGmail(t, func(w http.ResponseWriter, _ *http.Request) {
		conn, buffered, err := w.(http.Hijacker).Hijack()
		if !assert.NoError(t, err) {
			return
		}
		defer conn.Close()
		_, _ = buffered.WriteString("HTTP/1.1 200 OK\r\nContent-Length: secret-page-body\r\n\r\n")
		_ = buffered.Flush()
	})

	_, err := client.Mailbox(t.Context(), connectedaccount.NewAccessToken("access-1"))

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret-page-body")
}

// Google's client logs each request's body at its debug level; inbok's
// Gmail client logs nothing, so no message reaches stderr.
func TestSendNoSDKLog(t *testing.T) {
	t.Setenv("GOOGLE_SDK_GO_LOGGING_LEVEL", "debug")
	client := stubGmail(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"id": "gm-1", "threadId": "th-1"})
	})
	read, write, err := os.Pipe()
	require.NoError(t, err)
	stderr := os.Stderr
	os.Stderr = write
	t.Cleanup(func() { os.Stderr = stderr })

	_, err = client.Send(t.Context(), connectedaccount.NewAccessToken("access-1"), rawMessage("<a@b>"))
	os.Stderr = stderr
	require.NoError(t, write.Close())
	logged, readErr := io.ReadAll(read)

	require.NoError(t, err)
	require.NoError(t, readErr)
	assert.Empty(t, string(logged))
}

// A Gmail no connection reaches never had the message.
func TestSendUnreachable(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	server.Close()
	client := New(Settings{ClientID: testClientID, ClientSecret: testSecret, RedirectURL: testRedirect, BaseURL: base}, time.Now)

	_, err = client.Send(t.Context(), connectedaccount.NewAccessToken("access-1"), rawMessage("<a@b>"))

	require.ErrorIs(t, err, outgoingmessage.ErrNotSent)
}

// sentStub answers history.list with pages, each a list of message IDs
// added to Sent, and messages.get with each message's X-Inbok-Message-Id as
// headers names it; a message missing from headers answers 404.
func sentStub(t *testing.T, pages [][]string, headers map[string][2]string) *Client {
	t.Helper()
	return stubGmail(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/gmail/v1/users/me/history":
			assert.Equal(t, "1200", r.URL.Query().Get("startHistoryId"))
			assert.Equal(t, "messageAdded", r.URL.Query().Get("historyTypes"))
			assert.Equal(t, "SENT", r.URL.Query().Get("labelId"))
			page := 0
			if token := r.URL.Query().Get("pageToken"); token != "" {
				_, _ = fmt.Sscan(token, &page)
			}
			var history []map[string]any
			for _, id := range pages[page] {
				history = append(history, map[string]any{"id": "1", "messagesAdded": []any{
					map[string]any{"message": map[string]any{"id": id, "threadId": "th-" + id}},
				}})
			}
			answer := map[string]any{"history": history, "historyId": "1300"}
			if page+1 < len(pages) {
				answer["nextPageToken"] = fmt.Sprint(page + 1)
			}
			writeJSON(w, answer)
		case strings.HasPrefix(r.URL.Path, "/gmail/v1/users/me/messages/"):
			id := strings.TrimPrefix(r.URL.Path, "/gmail/v1/users/me/messages/")
			assert.Equal(t, "metadata", r.URL.Query().Get("format"))
			header, ok := headers[id]
			if !ok {
				gmailError(w, http.StatusNotFound, "Requested entity was not found.", "notFound")
				return
			}
			writeJSON(w, map[string]any{"id": id, "threadId": "th-" + id, "payload": map[string]any{
				"headers": []any{map[string]string{"name": header[0], "value": header[1]}},
			}})
		default:
			t.Errorf("unexpected request %s", r.URL)
		}
	})
}

// Gmail spells a header its own way; the check matches the name in any
// case and the value exactly, across pages.
func TestFindSent(t *testing.T) {
	client := sentStub(t, [][]string{{"gm-1"}, {"gm-2"}}, map[string][2]string{
		"gm-1": {"X-Inbok-Message-Id", "<other@example.com>"},
		"gm-2": {"x-inbok-message-id", "<mine@example.com>"},
	})

	found, err := client.FindSent(t.Context(), connectedaccount.NewAccessToken("access-1"), "1200", "<mine@example.com>")

	require.NoError(t, err)
	assert.Equal(t, outgoingmessage.Sent{ProviderMessageID: "gm-2", ProviderThreadID: "th-gm-2"}, found)
}

// A message deleted before the check might have been this one, so the
// check cannot say it was not sent, unless another one matches.
func TestFindSentDeleted(t *testing.T) {
	client := sentStub(t, [][]string{{"gm-gone", "gm-1"}}, map[string][2]string{
		"gm-1": {"X-Inbok-Message-Id", "<other@example.com>"},
	})
	token := connectedaccount.NewAccessToken("access-1")

	_, err := client.FindSent(t.Context(), token, "1200", "<mine@example.com>")
	require.ErrorIs(t, err, outgoingmessage.ErrCheckIncomplete)

	found, err := client.FindSent(t.Context(), token, "1200", "<other@example.com>")
	require.NoError(t, err)
	assert.Equal(t, "gm-1", found.ProviderMessageID)
}

// Gmail answers 404 for history it no longer keeps.
func TestFindSentExpired(t *testing.T) {
	client := stubGmail(t, func(w http.ResponseWriter, _ *http.Request) {
		gmailError(w, http.StatusNotFound, "Requested entity was not found.", "notFound")
	})

	_, err := client.FindSent(t.Context(), connectedaccount.NewAccessToken("access-1"), "1200", "<mine@example.com>")

	require.ErrorIs(t, err, outgoingmessage.ErrCheckIncomplete)
}

// A mailbox that sent more than the check reads is not searched further.
func TestFindSentTooMany(t *testing.T) {
	pages := make([][]string, maxCheckPages+1)
	headers := map[string][2]string{}
	for i := range pages {
		id := fmt.Sprintf("gm-%d", i)
		pages[i] = []string{id}
		headers[id] = [2]string{"X-Inbok-Message-Id", "<other@example.com>"}
	}
	client := sentStub(t, pages, headers)

	_, err := client.FindSent(t.Context(), connectedaccount.NewAccessToken("access-1"), "1200", "<mine@example.com>")

	require.ErrorIs(t, err, outgoingmessage.ErrCheckIncomplete)
}

// A check that fails for a reason that may pass says neither found nor
// not found, and keeps none of Gmail's text.
func TestFindSentFails(t *testing.T) {
	client := stubGmail(t, func(w http.ResponseWriter, _ *http.Request) {
		gmailError(w, http.StatusServiceUnavailable, "secret page body", "backendError")
	})

	_, err := client.FindSent(t.Context(), connectedaccount.NewAccessToken("access-1"), "1200", "<mine@example.com>")

	require.Error(t, err)
	require.NotErrorIs(t, err, outgoingmessage.ErrSentNotFound)
	require.NotErrorIs(t, err, outgoingmessage.ErrCheckIncomplete)
	assert.NotContains(t, err.Error(), "secret page body")
}
