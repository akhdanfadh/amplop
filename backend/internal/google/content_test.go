package google

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/googleemulator"
	"amplop/internal/mailsync"
	"amplop/internal/messagecontent"
)

// withAttachment is a message whose text comes in the answer and whose
// attachment Gmail sends by attachmentId.
const withAttachment = "From: a@example.com\r\n" +
	"To: alex@example.com\r\n" +
	"Subject: Report\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=b1\r\n" +
	"\r\n" +
	"--b1\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"See attached.\r\n" +
	"--b1\r\n" +
	"Content-Type: application/pdf; name=\"report.pdf\"\r\n" +
	"Content-Disposition: attachment; filename=\"report.pdf\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"JVBERi0xLjQK\r\n" +
	"--b1--\r\n"

// The full format comes back as the part tree [messagecontent.Parse]
// reads: text parts with their content, an attachment without it.
func TestMessage(t *testing.T) {
	client, token := startMailbox(t, nil, []googleemulator.Message{
		{ID: "alt", Mailbox: alexMailbox, From: "a@example.com", Subject: "Hi", Body: "Hello", HTML: "<p>Hello</p>"},
		{ID: "att", Mailbox: alexMailbox, From: "a@example.com", Subject: "Report", Raw: withAttachment},
	})

	alt, err := client.Message(t.Context(), token, testAccountID, "alt")
	require.NoError(t, err)
	require.Len(t, alt.Parts, 2)
	assert.Equal(t, "multipart/alternative", alt.MediaType)
	assert.Equal(t, []byte("Hello"), alt.Parts[0].Content)
	assert.Equal(t, []byte("<p>Hello</p>"), alt.Parts[1].Content)

	att, err := client.Message(t.Context(), token, testAccountID, "att")
	require.NoError(t, err)
	require.Len(t, att.Parts, 2)
	assert.Equal(t, "multipart/mixed", att.MediaType)
	assert.Equal(t, "report.pdf", att.Parts[1].Filename)
	assert.Nil(t, att.Parts[1].Content, "sent by attachmentId")
}

// A part's content comes from the answer's data, or else from its
// attachment, fetched by a fresh attachmentId.
func TestPartContent(t *testing.T) {
	client, token := startMailbox(t, nil, []googleemulator.Message{
		{ID: "att", Mailbox: alexMailbox, From: "a@example.com", Subject: "Report", Raw: withAttachment},
	})
	received, err := client.Message(t.Context(), token, testAccountID, "att")
	require.NoError(t, err)

	text, charset, err := client.PartContent(t.Context(), token, testAccountID, "att", received.Parts[0].ID)
	require.NoError(t, err)
	assert.Equal(t, "See attached.", strings.TrimSpace(string(text)))
	assert.Equal(t, "utf-8", charset, "a text part's inline data")

	pdf, charset, err := client.PartContent(t.Context(), token, testAccountID, "att", received.Parts[1].ID)
	require.NoError(t, err)
	assert.Equal(t, []byte("%PDF-1.4\n"), pdf)
	assert.Empty(t, charset, "bytes by attachmentId come as sent")
}

// A message or part Gmail does not have is gone.
func TestContentGone(t *testing.T) {
	client, token := startMailbox(t, nil, []googleemulator.Message{
		{ID: "m1", Mailbox: alexMailbox, From: "a@example.com", Subject: "Hi", Body: "Hello"},
	})

	_, err := client.Message(t.Context(), token, testAccountID, "missing")
	require.ErrorIs(t, err, messagecontent.ErrGone)
	_, _, err = client.PartContent(t.Context(), token, testAccountID, "missing", "0")
	require.ErrorIs(t, err, messagecontent.ErrGone)
	_, _, err = client.PartContent(t.Context(), token, testAccountID, "m1", "9.9")
	require.ErrorIs(t, err, messagecontent.ErrGone)
}

// An answer past its cap is too large, and data that is not base64url is
// unreadable; neither quotes Gmail's answer.
func TestContentRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		payload map[string]any
		want    error
	}{
		"too large": {
			payload: map[string]any{"partId": "", "mimeType": "text/plain", "body": map[string]any{"data": strings.Repeat("a", fullResponseBytes)}},
			want:    messagecontent.ErrTooLarge,
		},
		"unreadable": {
			payload: map[string]any{"partId": "", "mimeType": "text/plain", "body": map[string]any{"data": "secret!*"}},
			want:    messagecontent.ErrUnreadable,
		},
	} {
		t.Run(name, func(t *testing.T) {
			client, _ := stubGmail(t, func(w http.ResponseWriter, _ *http.Request) {
				gmailMessage(w, map[string]any{"id": "m1", "payload": tc.payload})
			})

			_, err := client.Message(t.Context(), stubToken, testAccountID, "m1")

			require.ErrorIs(t, err, tc.want)
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}

// Answers Gmail sends whole but that do not decode, have no value, or
// refuse this one message are unreadable, for messages.get and
// messages.attachments.get alike; none quotes Gmail's answer.
func TestContentUnreadable(t *testing.T) {
	full := `{"id":"m1","payload":{"partId":"","mimeType":"multipart/mixed","parts":[` +
		`{"partId":"0","mimeType":"application/pdf","filename":"a.pdf","body":{"attachmentId":"att-1","size":3}}]}}`
	for name, answer := range map[string]func(http.ResponseWriter){
		"not json": func(w http.ResponseWriter) { writeRaw(w, http.StatusOK, `{"secret": `) },
		"wrong type": func(w http.ResponseWriter) {
			writeRaw(w, http.StatusOK, `{"data":true,"payload":{"body":{"data":true}},"secret":1}`)
		},
		"null":          func(w http.ResponseWriter) { writeRaw(w, http.StatusOK, `null`) },
		"no content":    func(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) },
		"bad request":   func(w http.ResponseWriter) { refuse(w, http.StatusBadRequest, "invalidArgument") },
		"unprocessable": func(w http.ResponseWriter) { refuse(w, http.StatusUnprocessableEntity, "secret") },
	} {
		t.Run(name, func(t *testing.T) {
			client, _ := stubGmail(t, func(w http.ResponseWriter, _ *http.Request) { answer(w) })
			_, err := client.Message(t.Context(), stubToken, testAccountID, "m1")
			require.ErrorIs(t, err, messagecontent.ErrUnreadable)
			assert.NotContains(t, err.Error(), "secret")

			// The attachment's own answer, after a good messages.get.
			client, _ = stubGmail(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/attachments/") {
					answer(w)
					return
				}
				writeRaw(w, http.StatusOK, full)
			})
			_, _, err = client.PartContent(t.Context(), stubToken, testAccountID, "m1", "0")
			require.ErrorIs(t, err, messagecontent.ErrUnreadable)
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}

// An answer about another message than the one asked for is unreadable.
func TestContentAnotherMessage(t *testing.T) {
	client, _ := stubGmail(t, func(w http.ResponseWriter, _ *http.Request) {
		writeRaw(w, http.StatusOK, `{"id":"m2","payload":{"partId":"","mimeType":"text/plain"}}`)
	})

	_, err := client.Message(t.Context(), stubToken, testAccountID, "m1")

	require.ErrorIs(t, err, messagecontent.ErrUnreadable)
}

// A 410 is gone too, while refusals that concern the account or the
// moment, and an answer cut off on the way, stay errors that may pass.
func TestContentNotForGood(t *testing.T) {
	client, _ := stubGmail(t, func(w http.ResponseWriter, _ *http.Request) { refuse(w, http.StatusGone, "gone") })
	_, err := client.Message(t.Context(), stubToken, testAccountID, "m1")
	require.ErrorIs(t, err, messagecontent.ErrGone)

	for name, answer := range map[string]func(http.ResponseWriter){
		"unauthorized": func(w http.ResponseWriter) { refuse(w, http.StatusUnauthorized, "authError") },
		"forbidden":    func(w http.ResponseWriter) { refuse(w, http.StatusForbidden, "insufficientPermissions") },
		"cut off": func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", "1000")
			_, _ = w.Write([]byte(`{"id":"m1","payload":{`))
		},
	} {
		t.Run(name, func(t *testing.T) {
			client, _ := stubGmail(t, func(w http.ResponseWriter, _ *http.Request) { answer(w) })

			_, err := client.Message(t.Context(), stubToken, testAccountID, "m1")

			require.Error(t, err)
			require.NotErrorIs(t, err, messagecontent.ErrUnreadable)
			require.NotErrorIs(t, err, messagecontent.ErrGone)
		})
	}
}

// writeJSON writes body as a JSON answer with status.
func writeRaw(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// A fetch whose wait for the account's pace would pass its deadline is
// refused at once as busy, sending nothing, while a context that ended
// returns its own error.
func TestContentPaceDeadline(t *testing.T) {
	var calls atomic.Int32
	client, _ := stubGmail(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		gmailMessage(w, map[string]any{"id": "m1", "payload": map[string]any{"partId": "", "mimeType": "text/plain"}})
	})
	_, err := client.Message(t.Context(), stubToken, testAccountID, "m1")
	require.NoError(t, err)

	short, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err = client.Message(short, stubToken, testAccountID, "m1")
	require.ErrorIs(t, err, mailsync.ErrProviderBusy)
	require.NoError(t, short.Err(), "refused before the deadline")
	assert.Equal(t, int32(1), calls.Load(), "the refused fetch sent nothing")

	ended, stop := context.WithCancel(t.Context())
	stop()
	_, err = client.Message(ended, stubToken, testAccountID, "m1")
	require.ErrorIs(t, err, context.Canceled)
}

// Replaying real Gmail's answers: a Latin-1 text file, which Gmail sends
// by attachmentId, comes as sent, its charset left to the caller, while
// the inline text beside it is already UTF-8.
func TestPartContentRecorded(t *testing.T) {
	message, err := os.ReadFile(filepath.Join("testdata", "recorded", "latin1-text-file.json"))
	require.NoError(t, err)
	attachment, err := os.ReadFile(filepath.Join("testdata", "recorded", "latin1-text-file.attachment.json"))
	require.NoError(t, err)
	client, _ := stubGmail(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/attachments/") {
			writeRaw(w, http.StatusOK, string(attachment))
			return
		}
		writeRaw(w, http.StatusOK, string(message))
	})

	notes, charset, err := client.PartContent(t.Context(), stubToken, testAccountID, "19a6a1f0c0d0e005", "1")
	require.NoError(t, err)
	assert.Equal(t, []byte("Caf\xe9 cr\xe8me, na\xefve fa\xe7ade.\n"), notes)
	assert.Empty(t, charset)

	text, charset, err := client.PartContent(t.Context(), stubToken, testAccountID, "19a6a1f0c0d0e005", "0")
	require.NoError(t, err)
	assert.Equal(t, "A Latin-1 text file is attached.", string(text))
	assert.Equal(t, "utf-8", charset)
}

// A full fetch asks for the full format and spends its units.
func TestMessageFormat(t *testing.T) {
	var format string
	client, _ := stubGmail(t, func(w http.ResponseWriter, r *http.Request) {
		format = r.URL.Query().Get("format")
		gmailMessage(w, map[string]any{"id": "m1", "payload": map[string]any{"partId": "", "mimeType": "text/plain"}})
	})

	_, err := client.Message(t.Context(), stubToken, testAccountID, "m1")

	require.NoError(t, err)
	assert.Equal(t, formatFull, format)
	pace := client.paces.take(testAccountID)
	defer client.paces.release(testAccountID)
	assert.InDelta(t, 0, pace.Tokens(), 1, "the full fetch took the bucket's 60 units")
}
