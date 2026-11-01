package systememail

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/emailaddress"
)

// testMailer sends with sender, decrypts with keys, and logs to logs.
func testMailer(storage *memoryStorage, keys *fakeKeyring, sender *fakeSender, logs *bytes.Buffer) *Mailer {
	return NewMailer(storage, keys, sender, slog.New(slog.NewTextHandler(logs, nil)))
}

func testOutgoing() Outgoing {
	return Outgoing{Purpose: "email verification", To: "admin@example.org", Subject: "Verify", Body: "Open the link.\n"}
}

func TestMailerSend(t *testing.T) {
	storage, sender := &memoryStorage{record: savedSignIn()}, &fakeSender{}

	revision, err := testMailer(storage, &fakeKeyring{}, sender, &bytes.Buffer{}).Send(t.Context(), testOutgoing())
	require.NoError(t, err)

	assert.Equal(t, int64(3), revision)
	assert.Equal(t, 1, sender.sent)
	assert.Equal(t, Server{Host: "smtp.example.com", Port: 587, Security: SecuritySTARTTLS, Username: "inbok@example.com"}, sender.server)
	assert.Equal(t, "app password", sender.password)
	assert.Equal(t, Message{
		FromAddress: "inbok@example.com", FromName: "inbok", To: "admin@example.org",
		Subject: "Verify", Body: "Open the link.\n",
	}, sender.message)
}

func TestMailerSendWithoutSignIn(t *testing.T) {
	record := savedSignIn()
	record.Username, record.Password = "", nil
	storage, sender := &memoryStorage{record: record}, &fakeSender{}

	_, err := testMailer(storage, &fakeKeyring{}, sender, &bytes.Buffer{}).Send(t.Context(), testOutgoing())
	require.NoError(t, err)

	assert.Empty(t, sender.server.Username)
	assert.Empty(t, sender.password)
}

// The recipient is sent to as emailaddress.Parse returns it.
func TestMailerSendRecipient(t *testing.T) {
	storage, sender := &memoryStorage{record: savedSignIn()}, &fakeSender{}
	outgoing := testOutgoing()
	outgoing.To = " Admin@Example.ORG\t"

	_, err := testMailer(storage, &fakeKeyring{}, sender, &bytes.Buffer{}).Send(t.Context(), outgoing)
	require.NoError(t, err)

	assert.Equal(t, "admin@example.org", sender.message.To)
}

// An invalid recipient is refused before the settings are read, so it comes
// with no revision.
func TestMailerSendInvalidRecipient(t *testing.T) {
	storage, sender := &memoryStorage{record: savedSignIn()}, &fakeSender{}
	outgoing := testOutgoing()
	outgoing.To = "admin@example.org, other@example.org"

	revision, err := testMailer(storage, &fakeKeyring{}, sender, &bytes.Buffer{}).Send(t.Context(), outgoing)
	require.ErrorIs(t, err, emailaddress.ErrInvalid)

	assert.Zero(t, revision)
	assert.Zero(t, sender.sent)
}

func TestMailerSendNotConfigured(t *testing.T) {
	sender := &fakeSender{}

	revision, err := testMailer(&memoryStorage{}, &fakeKeyring{}, sender, &bytes.Buffer{}).Send(t.Context(), testOutgoing())
	require.ErrorIs(t, err, ErrNotConfigured)

	assert.Zero(t, revision)
	assert.Zero(t, sender.sent)
}

func TestMailerSendReadFailure(t *testing.T) {
	failure := errors.New("database unavailable")
	sender := &fakeSender{}

	_, err := testMailer(&memoryStorage{err: failure}, &fakeKeyring{}, sender, &bytes.Buffer{}).Send(t.Context(), testOutgoing())
	require.ErrorIs(t, err, failure)

	assert.Zero(t, sender.sent)
}

// A password that cannot be decrypted is not sent, and the revision tells
// the caller which settings cannot send.
func TestMailerSendUndecryptable(t *testing.T) {
	record := savedSignIn()
	record.Password.KeyID = "2"
	sender := &fakeSender{}

	revision, err := testMailer(&memoryStorage{record: record}, &fakeKeyring{}, sender, &bytes.Buffer{}).Send(t.Context(), testOutgoing())
	require.ErrorIs(t, err, ErrPasswordUnreadable)

	assert.Equal(t, int64(3), revision)
	assert.Zero(t, sender.sent)
}

// A refused send is returned with the revision and logged with its purpose
// and the start of the server's reply, never the password.
func TestMailerSendFailed(t *testing.T) {
	storage := &memoryStorage{record: savedSignIn()}
	reply := "535 5.7.8 Authentication failed for inbok@example.com " + strings.Repeat("x", 2*maxLoggedReply)
	sender := &fakeSender{err: &SendError{Reason: ReasonAuth, Err: errors.New(reply)}}
	var logs bytes.Buffer
	var failed *SendError

	revision, err := testMailer(storage, &fakeKeyring{}, sender, &logs).Send(t.Context(), testOutgoing())
	require.ErrorAs(t, err, &failed)

	assert.Equal(t, ReasonAuth, failed.Reason)
	assert.Equal(t, int64(3), revision)
	assert.Contains(t, logs.String(), `msg="system email send failed" purpose="email verification" `+
		"reason=auth host=smtp.example.com port=587 security=starttls username=inbok@example.com")
	assert.Contains(t, logs.String(), "535 5.7.8")
	assert.NotContains(t, logs.String(), reply, "a long reply is cut")
	assert.NotContains(t, logs.String(), "app password")
}

// A private message's failure logs no reply, since a server can quote the
// message, here split by quoted-printable's line break.
func TestMailerSendFailedPrivate(t *testing.T) {
	storage := &memoryStorage{record: savedSignIn()}
	outgoing := testOutgoing()
	outgoing.Private = true
	sender := &fakeSender{err: &SendError{Reason: ReasonSender, Err: errors.New("554 blocked: verify-email#private-li=\r\nnk-secret")}}
	var logs bytes.Buffer

	_, err := testMailer(storage, &fakeKeyring{}, sender, &logs).Send(t.Context(), outgoing)
	require.Error(t, err)

	assert.Contains(t, logs.String(), `msg="system email send failed" purpose="email verification" reason=sender`)
	assert.NotContains(t, logs.String(), "554")
	assert.NotContains(t, logs.String(), "private-li")
}

// Errors the server did not cause, e.g. the caller giving up, are returned
// unchanged and not logged.
func TestMailerSendCanceled(t *testing.T) {
	sender := &fakeSender{err: context.Canceled}
	var logs bytes.Buffer

	revision, err := testMailer(&memoryStorage{record: savedSignIn()}, &fakeKeyring{}, sender, &logs).Send(t.Context(), testOutgoing())
	require.ErrorIs(t, err, context.Canceled)

	assert.Equal(t, int64(3), revision)
	assert.Empty(t, logs.String())
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "short", truncate("short", 10))
	assert.Equal(t, "abc…", truncate("abcdef", 3))
	assert.Equal(t, "a…", truncate("aé", 2), "a split character is dropped")
}
