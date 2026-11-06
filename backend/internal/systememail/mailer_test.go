package systememail

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/emailaddress"
	"inbok/internal/logging"
)

// testMailer sends with sender, decrypts with keys, and logs to logs as
// inbok's root logger does.
func testMailer(storage *memoryStorage, keys *fakeKeyring, sender *fakeSender, logs *bytes.Buffer) *Mailer {
	return NewMailer(storage, keys, sender, logging.New(logs))
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

// The recipient is sent to as [emailaddress.Parse] returns it.
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
	reply := "550 5.1.1 No such user here " + strings.Repeat("x", 2*maxLoggedReply)
	sender := &fakeSender{err: &SendError{Reason: ReasonRecipient, Err: errors.New(reply)}}
	var logs bytes.Buffer
	var failed *SendError

	ctx := logging.WithRequestID(t.Context(), "req-1")
	revision, err := testMailer(storage, &fakeKeyring{}, sender, &logs).Send(ctx, testOutgoing())
	require.ErrorAs(t, err, &failed)

	assert.Equal(t, ReasonRecipient, failed.Reason)
	assert.Equal(t, int64(3), revision)
	assert.Contains(t, logs.String(), `msg="system email send failed" purpose="email verification" `+
		"reason=recipient host=smtp.example.com port=587 security=starttls username=inbok@example.com")
	assert.Contains(t, logs.String(), "request_id=req-1", "the warning names the request that sent it")
	assert.Contains(t, logs.String(), "550 5.1.1")
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

// A message that requires tested settings is refused, unsent, when the
// settings it would use have not passed a test; with tested ones it sends.
func TestMailerSendRequireTested(t *testing.T) {
	untested := savedSignIn()
	untested.TestedAt = nil
	tested := savedSignIn()
	tested.TestedAt = new(time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC))
	outgoing := testOutgoing()
	outgoing.RequireTested = true

	refusedSender, sentSender := &fakeSender{}, &fakeSender{}
	_, refused := testMailer(&memoryStorage{record: untested}, &fakeKeyring{}, refusedSender, &bytes.Buffer{}).Send(t.Context(), outgoing)
	_, sent := testMailer(&memoryStorage{record: tested}, &fakeKeyring{}, sentSender, &bytes.Buffer{}).Send(t.Context(), outgoing)

	require.ErrorIs(t, refused, ErrNotTested)
	require.NoError(t, sent)
	assert.Equal(t, []int{0, 1}, []int{refusedSender.sent, sentSender.sent})
}

// A send the server may have accepted says so in the warning.
func TestMailerSendUnconfirmed(t *testing.T) {
	sender := &fakeSender{err: &SendError{Reason: ReasonTimeout, Err: errors.New("waiting at sender"), Unconfirmed: true}}
	var logs bytes.Buffer

	_, err := testMailer(&memoryStorage{record: savedSignIn()}, &fakeKeyring{}, sender, &logs).Send(t.Context(), testOutgoing())
	require.Error(t, err)

	assert.Contains(t, logs.String(), "reason=timeout host=smtp.example.com port=587 security=starttls username=inbok@example.com unconfirmed=true")
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
