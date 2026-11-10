package outgoingmessage

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/connectedaccount"
)

// sendNow is the clock of every send test.
var sendNow = time.Date(2026, 11, 9, 10, 31, 0, 0, time.UTC)

func (m *memoryStorage) ReadOutgoingMessageToSend(_ context.Context, messageID uuid.UUID) (Message, error) {
	for _, message := range m.messages {
		if message.ID == messageID {
			return message, nil
		}
	}
	return Message{}, ErrNotFound
}

func (m *memoryStorage) UpdateOutgoingMessage(_ context.Context, messageID uuid.UUID, update func(SendState) (Transition, error)) (Message, error) {
	for i, message := range m.messages {
		if message.ID != messageID {
			continue
		}
		next, err := update(SendState{Message: message, SenderDeactivated: m.deactivated})
		if err != nil {
			return Message{}, err
		}
		message.Status, message.Failure, message.SentAt = next.Status, next.Failure, next.SentAt
		message.ProviderMessageID, message.ProviderThreadID = next.Sent.ProviderMessageID, next.Sent.ProviderThreadID
		m.messages[i] = message
		if next.SenderEmail != "" {
			account := m.accounts[message.ConnectedAccountID]
			account.sender.Email = next.SenderEmail
			m.accounts[message.ConnectedAccountID] = account
		}
		return message, nil
	}
	return Message{}, ErrNotFound
}

// fakeTokens answers every Token with err, or a token.
type fakeTokens struct {
	err error
}

func (f *fakeTokens) Token(context.Context, int64) (connectedaccount.AccessToken, error) {
	if f.err != nil {
		return connectedaccount.AccessToken{}, f.err
	}
	return connectedaccount.NewAccessToken("access-1"), nil
}

// fakeProvider answers like Gmail as set, and records what it was asked.
type fakeProvider struct {
	mailbox    Mailbox
	mailboxErr error
	sent       Sent
	sendErr    error
	found      Sent
	findErr    error

	sends []string
	finds []string
}

func (f *fakeProvider) Mailbox(context.Context, connectedaccount.AccessToken) (Mailbox, error) {
	return f.mailbox, f.mailboxErr
}

func (f *fakeProvider) Send(_ context.Context, _ connectedaccount.AccessToken, raw []byte) (Sent, error) {
	f.sends = append(f.sends, string(raw))
	return f.sent, f.sendErr
}

func (f *fakeProvider) FindSent(_ context.Context, _ connectedaccount.AccessToken, historyID, inbokMessageID string) (Sent, error) {
	f.finds = append(f.finds, historyID+" "+inbokMessageID)
	return f.found, f.findErr
}

// sendTest is a saved message, waiting, with its storage, token access,
// Gmail, and the steps that send it.
type sendTest struct {
	storage  *memoryStorage
	tokens   *fakeTokens
	provider *fakeProvider
	sending  *Sending
	message  Message
}

func newSendTest(t *testing.T) *sendTest {
	t.Helper()
	storage := newMemoryStorage()
	message, err := NewService(storage).Create(t.Context(), 3, Draft{ConnectedAccountID: 5, Content: testContent()})
	require.NoError(t, err)
	tokens := &fakeTokens{}
	provider := &fakeProvider{
		mailbox: Mailbox{Email: "me@gmail.com", HistoryID: "1200"},
		sent:    Sent{ProviderMessageID: "gm-1", ProviderThreadID: "th-1"},
	}
	return &sendTest{
		storage:  storage,
		tokens:   tokens,
		provider: provider,
		sending:  NewSending(storage, tokens, provider, func() time.Time { return sendNow }),
		message:  message,
	}
}

// read returns the message as storage keeps it now.
func (s *sendTest) read(t *testing.T) Message {
	t.Helper()
	message, err := s.storage.ReadOutgoingMessageToSend(t.Context(), s.message.ID)
	require.NoError(t, err)
	return message
}

// setStatus saves status for the message, as an earlier step would have.
func (s *sendTest) setStatus(t *testing.T, status Status) {
	t.Helper()
	_, err := s.storage.UpdateOutgoingMessage(t.Context(), s.message.ID, func(SendState) (Transition, error) {
		return Transition{Status: status}, nil
	})
	require.NoError(t, err)
}

func TestPrepare(t *testing.T) {
	s := newSendTest(t)

	prepared, err := s.sending.Prepare(t.Context(), s.message.ID)

	require.NoError(t, err)
	assert.Equal(t, Preparation{Status: StatusWaiting, HistoryID: "1200"}, prepared)
	assert.Equal(t, StatusWaiting, s.read(t).Status)
}

// Each error that stops all work for the account ends the message with
// its own failure.
func TestPrepareStopErrors(t *testing.T) {
	cases := []struct {
		err  error
		want Failure
	}{
		{connectedaccount.ErrNeedsReconnect, FailureNeedsReconnect},
		{connectedaccount.ErrTokenUnreadable, FailureTokenUnreadable},
		{connectedaccount.ErrDeactivated, FailureDeactivated},
	}
	for _, c := range cases {
		t.Run(string(c.want), func(t *testing.T) {
			s := newSendTest(t)
			s.tokens.err = c.err

			prepared, err := s.sending.Prepare(t.Context(), s.message.ID)

			require.NoError(t, err)
			assert.Equal(t, StatusFailed, prepared.Status)
			assert.Equal(t, c.want, s.read(t).Failure)
		})
	}
}

// An error that may pass leaves the message waiting for the retry.
func TestPrepareRetryable(t *testing.T) {
	cases := map[string]func(*sendTest){
		"token":   func(s *sendTest) { s.tokens.err = errors.New("google unavailable") },
		"mailbox": func(s *sendTest) { s.provider.mailboxErr = errors.New("gmail unavailable") },
	}
	for name, set := range cases {
		t.Run(name, func(t *testing.T) {
			s := newSendTest(t)
			set(s)

			_, err := s.sending.Prepare(t.Context(), s.message.ID)
			require.Error(t, err)
			_, err = s.sending.Send(t.Context(), s.message.ID)

			require.Error(t, err)
			assert.Equal(t, StatusWaiting, s.read(t).Status)
		})
	}
}

// A message whose account was disconnected is gone; nothing is left to do.
func TestPrepareGone(t *testing.T) {
	s := newSendTest(t)
	s.tokens.err = connectedaccount.ErrNotFound

	prepared, err := s.sending.Prepare(t.Context(), s.message.ID)
	require.NoError(t, err)
	assert.Equal(t, Preparation{}, prepared)

	prepared, err = s.sending.Prepare(t.Context(), uuid.New())
	require.NoError(t, err)
	assert.Equal(t, Preparation{}, prepared)
}

func TestSend(t *testing.T) {
	s := newSendTest(t)

	status, err := s.sending.Send(t.Context(), s.message.ID)

	require.NoError(t, err)
	assert.Equal(t, StatusSent, status)
	saved := s.read(t)
	assert.Equal(t, "gm-1", saved.ProviderMessageID)
	assert.Equal(t, "th-1", saved.ProviderThreadID)
	require.NotNil(t, saved.SentAt)
	assert.Equal(t, sendNow, *saved.SentAt)
	require.Len(t, s.provider.sends, 1)
	assert.Contains(t, s.provider.sends[0], "X-Inbok-Message-Id: "+s.message.InbokMessageID)
}

// Gmail's answers that say the message was not sent end it failed.
func TestSendRefused(t *testing.T) {
	cases := []struct {
		err  error
		want Failure
	}{
		{ErrRefused, FailureRefused},
		{ErrSendingLimit, FailureSendingLimit},
	}
	for _, c := range cases {
		t.Run(string(c.want), func(t *testing.T) {
			s := newSendTest(t)
			s.provider.sendErr = c.err

			status, err := s.sending.Send(t.Context(), s.message.ID)

			require.NoError(t, err)
			assert.Equal(t, StatusFailed, status)
			assert.Equal(t, c.want, s.read(t).Failure)
		})
	}
}

// Gmail's address may differ in letter case only: the same account.
func TestSendAddressCase(t *testing.T) {
	s := newSendTest(t)
	s.provider.mailbox.Email = "Me@Gmail.com"

	status, err := s.sending.Send(t.Context(), s.message.ID)

	require.NoError(t, err)
	assert.Equal(t, StatusSent, status)
}

// A renamed account is not sent from: the message fails, and the account
// takes Gmail's address, so the member's next message shows it. The step
// that sends checks, so a rename after an earlier step is caught too.
func TestSendSenderChanged(t *testing.T) {
	s := newSendTest(t)
	prepared, err := s.sending.Prepare(t.Context(), s.message.ID)
	require.NoError(t, err)
	require.Equal(t, StatusWaiting, prepared.Status)
	s.provider.mailbox.Email = "Me.Renamed@gmail.com"

	status, err := s.sending.Send(t.Context(), s.message.ID)

	require.NoError(t, err)
	assert.Equal(t, StatusFailed, status)
	assert.Equal(t, FailureSenderChanged, s.read(t).Failure)
	assert.Equal(t, "me.renamed@gmail.com", s.storage.accounts[5].sender.Email)
	assert.Empty(t, s.provider.sends)
}

// A member deactivated after the token was taken, read again under the
// message's lock, stops the send (ADR029).
func TestSendDeactivated(t *testing.T) {
	s := newSendTest(t)
	s.storage.deactivated = true

	status, err := s.sending.Send(t.Context(), s.message.ID)

	require.NoError(t, err)
	assert.Equal(t, StatusFailed, status)
	assert.Equal(t, FailureDeactivated, s.read(t).Failure)
	assert.Empty(t, s.provider.sends)
}

// A send that did not reach Gmail leaves the message waiting, so the retry
// sends it.
func TestSendNotSent(t *testing.T) {
	s := newSendTest(t)
	s.provider.sendErr = ErrNotSent

	_, err := s.sending.Send(t.Context(), s.message.ID)

	require.ErrorIs(t, err, ErrNotSent)
	assert.Equal(t, StatusWaiting, s.read(t).Status)
}

// An unanswered send leaves the message sending for the check, and a
// repeat of the step never sends again.
func TestSendUnanswered(t *testing.T) {
	s := newSendTest(t)
	s.provider.sendErr = ErrUnanswered

	status, err := s.sending.Send(t.Context(), s.message.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusSending, status)

	status, err = s.sending.Send(t.Context(), s.message.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusSending, status)
	assert.Len(t, s.provider.sends, 1, "a message that may have been sent is checked, not sent again")
}

// A step repeated after a crash between Gmail's answer and saving it finds
// the message sending and sends nothing.
func TestSendRepeatAfterCrash(t *testing.T) {
	s := newSendTest(t)
	s.setStatus(t, StatusSending)

	status, err := s.sending.Send(t.Context(), s.message.ID)

	require.NoError(t, err)
	assert.Equal(t, StatusSending, status)
	assert.Empty(t, s.provider.sends)
}

// A message that ended is left as it is.
func TestSendEnded(t *testing.T) {
	s := newSendTest(t)
	_, err := s.sending.Send(t.Context(), s.message.ID)
	require.NoError(t, err)

	status, err := s.sending.Send(t.Context(), s.message.ID)

	require.NoError(t, err)
	assert.Equal(t, StatusSent, status)
	assert.Len(t, s.provider.sends, 1)
}

// A stop error before sending ends the message failed, with nothing sent.
func TestSendStopError(t *testing.T) {
	s := newSendTest(t)
	s.tokens.err = connectedaccount.ErrNeedsReconnect

	status, err := s.sending.Send(t.Context(), s.message.ID)

	require.NoError(t, err)
	assert.Equal(t, StatusFailed, status)
	assert.Equal(t, FailureNeedsReconnect, s.read(t).Failure)
	assert.Empty(t, s.provider.sends)
}

func TestCheckFound(t *testing.T) {
	s := newSendTest(t)
	s.setStatus(t, StatusSending)
	s.provider.found = Sent{ProviderMessageID: "gm-2", ProviderThreadID: "th-2"}

	status, err := s.sending.Check(t.Context(), s.message.ID, "1200")

	require.NoError(t, err)
	assert.Equal(t, StatusSent, status)
	assert.Equal(t, "gm-2", s.read(t).ProviderMessageID)
	assert.Equal(t, []string{"1200 " + s.message.InbokMessageID}, s.provider.finds)
}

// Not finding the message, or not being able to tell, ends it unknown:
// never sent again (ADR006).
func TestCheckNotFound(t *testing.T) {
	for _, findErr := range []error{ErrSentNotFound, ErrCheckIncomplete} {
		t.Run(findErr.Error(), func(t *testing.T) {
			s := newSendTest(t)
			s.setStatus(t, StatusSending)
			s.provider.findErr = findErr

			status, err := s.sending.Check(t.Context(), s.message.ID, "1200")

			require.NoError(t, err)
			assert.Equal(t, StatusUnknown, status)
			assert.Empty(t, s.provider.sends)
		})
	}
}

// Without a token the check cannot tell, so the message ends unknown.
func TestCheckStopError(t *testing.T) {
	s := newSendTest(t)
	s.setStatus(t, StatusSending)
	s.tokens.err = connectedaccount.ErrDeactivated

	status, err := s.sending.Check(t.Context(), s.message.ID, "1200")

	require.NoError(t, err)
	assert.Equal(t, StatusUnknown, status)
	assert.Empty(t, s.read(t).Failure)
}

// A check that fails for a reason that may pass is retried, the message
// still sending.
func TestCheckRetryable(t *testing.T) {
	s := newSendTest(t)
	s.setStatus(t, StatusSending)
	s.provider.findErr = errors.New("gmail unavailable")

	_, err := s.sending.Check(t.Context(), s.message.ID, "1200")

	require.Error(t, err)
	assert.Equal(t, StatusSending, s.read(t).Status)
}

// Giving up fails a waiting message, which was not sent, and leaves a
// sending one unknown, which may have been.
func TestGiveUp(t *testing.T) {
	cases := []struct {
		from    Status
		want    Status
		failure Failure
	}{
		{StatusWaiting, StatusFailed, FailureGaveUp},
		{StatusSending, StatusUnknown, ""},
	}
	for _, c := range cases {
		t.Run(string(c.from), func(t *testing.T) {
			s := newSendTest(t)
			s.setStatus(t, c.from)

			status, err := s.sending.GiveUp(t.Context(), s.message.ID)

			require.NoError(t, err)
			assert.Equal(t, c.want, status)
			assert.Equal(t, c.failure, s.read(t).Failure)
		})
	}
}
