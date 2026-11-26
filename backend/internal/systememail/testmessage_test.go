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

	"amplop/internal/emailaddress"
)

// fakeSender records each send and answers with err. During, if set, runs
// while the message is being sent.
type fakeSender struct {
	err    error
	during func()

	sent     int
	server   Server
	password string
	message  Message
}

func (f *fakeSender) Send(_ context.Context, server Server, password string, message Message) error {
	f.sent++
	f.server, f.password, f.message = server, password, message
	if f.during != nil {
		f.during()
	}
	return f.err
}

var testNow = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

// testMessages sends with sender, decrypts with keys, and logs to logs.
func testMessages(storage *memoryStorage, keys *fakeKeyring, sender *fakeSender, logs *bytes.Buffer) *TestMessages {
	return NewTestMessages(storage, testMailer(storage, keys, sender, logs), func() time.Time { return testNow })
}

// savedSignIn is [storedSignIn] with a password the fake keyring can open.
func savedSignIn() *Record {
	record := storedSignIn()
	record.Password = &Password{KeyID: "1", Ciphertext: []byte("sealed:" + signInOwner + ":app password")}
	return record
}

func TestSendTestMessageAccepted(t *testing.T) {
	storage, sender := &memoryStorage{record: savedSignIn()}, &fakeSender{}
	storage.record.TestedAt = nil

	result, err := testMessages(storage, &fakeKeyring{}, sender, &bytes.Buffer{}).Send(t.Context(), "admin@example.org")
	require.NoError(t, err)

	assert.Equal(t, TestMessageResult{Accepted: true}, result)
	assert.Equal(t, Server{Host: "smtp.example.com", Port: 587, Security: SecuritySTARTTLS, Username: "amplop@example.com"}, sender.server)
	assert.Equal(t, "app password", sender.password)
	assert.Equal(t, Message{
		FromAddress: "amplop@example.com", FromName: "amplop", To: "admin@example.org",
		Subject: testMessageSubject, Body: testMessageBody,
	}, sender.message)
	require.NotNil(t, storage.record.TestedAt)
	assert.Equal(t, testNow, *storage.record.TestedAt)
}

func TestSendTestMessageWithoutSignIn(t *testing.T) {
	record := savedSignIn()
	record.Username, record.Password = "", nil
	storage, sender := &memoryStorage{record: record}, &fakeSender{}

	result, err := testMessages(storage, &fakeKeyring{}, sender, &bytes.Buffer{}).Send(t.Context(), "admin@example.org")
	require.NoError(t, err)

	assert.True(t, result.Accepted)
	assert.Empty(t, sender.server.Username)
	assert.Empty(t, sender.password)
}

// A refused test is a result, not an error: it clears the tested time and
// logs the start of the server's reply.
func TestSendTestMessageFailed(t *testing.T) {
	storage := &memoryStorage{record: savedSignIn()}
	reply := "550 5.1.1 No such user here " + strings.Repeat("x", 2*maxLoggedReply)
	sender := &fakeSender{err: &SendError{Reason: ReasonRecipient, Err: errors.New(reply)}}
	var logs bytes.Buffer

	result, err := testMessages(storage, &fakeKeyring{}, sender, &logs).Send(t.Context(), "admin@example.org")
	require.NoError(t, err)

	assert.Equal(t, TestMessageResult{Reason: ReasonRecipient}, result)
	assert.Nil(t, storage.record.TestedAt)
	assert.Contains(t, logs.String(), `purpose="test message" reason=recipient host=smtp.example.com port=587 security=starttls username=amplop@example.com`)
	assert.Contains(t, logs.String(), "550 5.1.1")
	assert.NotContains(t, logs.String(), reply, "a long reply is cut")
}

// An invalid recipient sends nothing and leaves the tested time.
func TestSendTestMessageInvalidRecipient(t *testing.T) {
	saved := savedSignIn()
	storage, sender := &memoryStorage{record: saved}, &fakeSender{}

	result, err := testMessages(storage, &fakeKeyring{}, sender, &bytes.Buffer{}).Send(t.Context(), "Admin <admin@example.org>")
	require.ErrorIs(t, err, emailaddress.ErrInvalid)

	assert.Empty(t, result)
	assert.Zero(t, sender.sent)
	assert.Same(t, saved, storage.record)
}

func TestSendTestMessageNotConfigured(t *testing.T) {
	sender := &fakeSender{}

	result, err := testMessages(&memoryStorage{}, &fakeKeyring{}, sender, &bytes.Buffer{}).Send(t.Context(), "admin@example.org")
	require.ErrorIs(t, err, ErrNotConfigured)

	assert.Empty(t, result)
	assert.Zero(t, sender.sent)
}

// Errors the server did not cause are returned and leave the tested time.
func TestSendTestMessageError(t *testing.T) {
	failure := errors.New("unavailable")
	for _, c := range []struct {
		name    string
		storage *memoryStorage
		sender  *fakeSender
		sent    int
	}{
		{"read", &memoryStorage{err: failure}, &fakeSender{}, 0},
		{"send", &memoryStorage{record: savedSignIn()}, &fakeSender{err: context.Canceled}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			before := c.storage.record

			result, err := testMessages(c.storage, &fakeKeyring{}, c.sender, &bytes.Buffer{}).Send(t.Context(), "admin@example.org")
			require.Error(t, err)

			assert.Empty(t, result)
			assert.Equal(t, c.sent, c.sender.sent)
			assert.Same(t, before, c.storage.record, "the tested time is unchanged")
		})
	}
}

// A password that cannot be decrypted is not sent, and the settings lose
// their tested time: they cannot send.
func TestSendTestMessageUndecryptable(t *testing.T) {
	for _, c := range []struct {
		name   string
		change func(*Record)
	}{
		{"unknown key", func(r *Record) { r.Password.KeyID = "2" }},
		{"moved to another server", func(r *Record) { r.Host = "smtp.attacker.example" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			record := savedSignIn()
			c.change(record)
			storage, sender := &memoryStorage{record: record}, &fakeSender{}

			result, err := testMessages(storage, &fakeKeyring{}, sender, &bytes.Buffer{}).Send(t.Context(), "admin@example.org")
			require.ErrorIs(t, err, ErrPasswordUnreadable)

			assert.Empty(t, result)
			assert.Zero(t, sender.sent)
			assert.Nil(t, storage.record.TestedAt)
		})
	}
}

// A save while the message is being sent replaced what was tested, so the
// result is returned but the saved settings keep their own tested time.
func TestSendTestMessageSavedDuring(t *testing.T) {
	newer := time.Date(2026, 10, 30, 11, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name string
		err  error
	}{
		{"accepted", nil},
		{"failed", &SendError{Reason: ReasonRecipient, Err: errors.New("550 No such user")}},
	} {
		t.Run(c.name, func(t *testing.T) {
			storage := &memoryStorage{record: savedSignIn()}
			// Other settings were saved and passed a test of their own.
			sender := &fakeSender{err: c.err, during: func() {
				saved := *storage.record
				saved.FromName, saved.TestedAt, saved.Revision = "Changed", &newer, saved.Revision+1
				storage.record = &saved
			}}

			result, err := testMessages(storage, &fakeKeyring{}, sender, &bytes.Buffer{}).Send(t.Context(), "admin@example.org")
			require.NoError(t, err)

			assert.Equal(t, c.err == nil, result.Accepted)
			assert.Equal(t, "Changed", storage.record.FromName)
			assert.Equal(t, &newer, storage.record.TestedAt)
		})
	}
}

// Recording the result can fail after the server accepted the message.
func TestSendTestMessageRecordFailure(t *testing.T) {
	storage := &memoryStorage{record: savedSignIn()}
	failure := errors.New("database unavailable")
	sender := &fakeSender{during: func() { storage.err = failure }}

	result, err := testMessages(storage, &fakeKeyring{}, sender, &bytes.Buffer{}).Send(t.Context(), "admin@example.org")
	require.ErrorIs(t, err, failure)

	assert.Empty(t, result)
	assert.Equal(t, 1, sender.sent)
}
