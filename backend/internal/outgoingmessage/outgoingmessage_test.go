package outgoingmessage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/attemptlimit"
	"inbok/internal/connectedaccount"
)

// memoryAccount is a connected account as storage reads it for a send.
type memoryAccount struct {
	userID int64
	sender Sender
}

// memoryStorage keeps messages like the table: a message belongs to its
// account's owner.
type memoryStorage struct {
	accounts map[int64]memoryAccount
	messages []Message
	// keys are the messages saved with each member's keys, and the requests
	// that saved them.
	keys map[memoryKey]memoryKeyed
	// answered are the members' synchronized messages a reply may answer.
	answered map[uuid.UUID]Answered
	// attempts are every member's send attempts, by member.
	attempts map[int64][]time.Time
	// buildCalls counts calls to build, which run under the account's lock.
	buildCalls int
	// deactivated is whether every account's member is deactivated.
	deactivated bool
	// beforeKeyRead runs before ReadOutgoingMessageByKey, e.g. to disconnect.
	beforeKeyRead func()
}

func newMemoryStorage() *memoryStorage {
	return &memoryStorage{
		accounts: map[int64]memoryAccount{
			5: {userID: 3, sender: Sender{ConnectedAccountID: 5, Email: "me@gmail.com"}},
			6: {userID: 3, sender: Sender{ConnectedAccountID: 6, Email: "stale@gmail.com", NeedsReconnect: true}},
			9: {userID: 3, sender: Sender{ConnectedAccountID: 9, Email: "lost@gmail.com", Credential: connectedaccount.Credential{KeyID: lostKeyID}}},
			8: {userID: 4, sender: Sender{ConnectedAccountID: 8, Email: "other@gmail.com"}},
		},
		keys:     map[memoryKey]memoryKeyed{},
		answered: map[uuid.UUID]Answered{},
		attempts: map[int64][]time.Time{},
	}
}

func (m *memoryStorage) InsertOutgoingMessage(_ context.Context, attempt Attempt, request Request, build func(CreateState) (NewMessage, error)) (Message, error) {
	if m.deactivated {
		return Message{}, ErrDeactivated
	}
	if _, ok := m.keys[memoryKey{attempt.UserID, request.Key}]; ok {
		return Message{}, ErrKeyExists
	}
	account, ok := m.accounts[request.AccountID]
	if !ok || account.userID != attempt.UserID {
		return Message{}, ErrAccountNotFound
	}
	state := CreateState{Sender: account.sender, Attempts: m.attemptsSince(attempt)}
	if request.AnsweredID != nil {
		answered, ok := m.answered[*request.AnsweredID]
		if !ok || m.accounts[answered.ConnectedAccountID].userID != attempt.UserID {
			return Message{}, ErrAnsweredNotFound
		}
		state.Answered = &answered
	}
	m.buildCalls++
	next, err := build(state)
	if err != nil {
		return Message{}, err
	}
	saved := m.save(attempt, request.AccountID, next)
	m.keys[memoryKey{attempt.UserID, request.Key}] = memoryKeyed{messageID: saved.ID, request: request}
	return saved, nil
}

// memoryKey is one member's idempotency key.
type memoryKey struct {
	userID int64
	key    uuid.UUID
}

// memoryKeyed is the message a key saved, and the request that saved it.
type memoryKeyed struct {
	messageID uuid.UUID
	request   Request
}

func (m *memoryStorage) InsertOutgoingMessageCopy(ctx context.Context, attempt Attempt, messageID uuid.UUID, build func(CopyState) (NewMessage, error)) (Message, error) {
	if m.deactivated {
		return Message{}, ErrDeactivated
	}
	original, err := m.ReadOutgoingMessage(ctx, attempt.UserID, messageID)
	if err != nil {
		return Message{}, err
	}
	if original.SentAgainAs != nil {
		return m.ReadOutgoingMessage(ctx, attempt.UserID, *original.SentAgainAs)
	}
	m.buildCalls++
	sender := m.accounts[original.ConnectedAccountID].sender
	next, err := build(CopyState{Original: original, Sender: sender, Attempts: m.attemptsSince(attempt)})
	if err != nil {
		return Message{}, err
	}
	copied := m.save(attempt, original.ConnectedAccountID, next)
	for i := range m.messages {
		if m.messages[i].ID == messageID {
			m.messages[i].SentAgainAs = &copied.ID
		}
	}
	return copied, nil
}

// attemptsSince returns the member's attempts after attempt.Since.
func (m *memoryStorage) attemptsSince(attempt Attempt) []time.Time {
	var counted []time.Time
	for _, at := range m.attempts[attempt.UserID] {
		if at.After(attempt.Since) {
			counted = append(counted, at)
		}
	}
	return counted
}

// save keeps next as a waiting message, and counts attempt.
func (m *memoryStorage) save(attempt Attempt, accountID int64, next NewMessage) Message {
	saved := Message{
		ID:                 uuid.New(),
		ConnectedAccountID: accountID,
		From:               next.From,
		Content:            next.Content,
		InbokMessageID:     next.InbokMessageID,
		Threading:          next.Threading,
		ProviderThreadID:   next.ProviderThreadID,
		Status:             StatusWaiting,
		CreatedAt:          attempt.At,
	}
	m.messages = append(m.messages, saved)
	m.attempts[attempt.UserID] = append(m.attempts[attempt.UserID], attempt.At)
	return saved
}

func (m *memoryStorage) ReadOutgoingMessage(_ context.Context, userID int64, messageID uuid.UUID) (Message, error) {
	for _, message := range m.messages {
		if message.ID == messageID && m.accounts[message.ConnectedAccountID].userID == userID {
			return message, nil
		}
	}
	return Message{}, ErrNotFound
}

func (m *memoryStorage) ReadOutgoingMessageByKey(ctx context.Context, userID int64, key uuid.UUID) (Message, Request, error) {
	if m.beforeKeyRead != nil {
		m.beforeKeyRead()
	}
	keyed, ok := m.keys[memoryKey{userID, key}]
	if !ok {
		return Message{}, Request{}, ErrNotFound
	}
	message, err := m.ReadOutgoingMessage(ctx, userID, keyed.messageID)
	return message, keyed.request, err
}

// ListOutgoingMessages orders as the query does: outstanding first, then
// newest first, ties by the larger ID.
func (m *memoryStorage) ListOutgoingMessages(_ context.Context, userID int64, limit int) ([]Message, error) {
	var listed []Message
	for _, message := range m.messages {
		if m.accounts[message.ConnectedAccountID].userID == userID {
			message.Body = ""
			listed = append(listed, message)
		}
	}
	slices.SortFunc(listed, func(a, b Message) int {
		if a.Outstanding() != b.Outstanding() {
			if a.Outstanding() {
				return -1
			}
			return 1
		}
		if order := b.CreatedAt.Compare(a.CreatedAt); order != 0 {
			return order
		}
		return bytes.Compare(b.ID[:], a.ID[:])
	})
	return listed[:min(limit, len(listed))], nil
}

// createNow is the clock of every create test.
var createNow = time.Date(2026, 11, 9, 10, 30, 0, 0, time.UTC)

// lostKeyID is the ID of a key [memoryAccounts] no longer has.
const lostKeyID = "lost"

// memoryAccounts reads storage's accounts as [connectedaccount.Service]
// does: it opens every token except one sealed with [lostKeyID], as a key
// list without that key would, and gives each account its status.
type memoryAccounts struct {
	storage *memoryStorage
}

func (memoryAccounts) CheckToken(_, _ int64, credential connectedaccount.Credential) error {
	if credential.KeyID == lostKeyID {
		return fmt.Errorf("%w: no key %s", connectedaccount.ErrTokenUnreadable, lostKeyID)
	}
	return nil
}

func (a memoryAccounts) List(_ context.Context, userID int64) ([]connectedaccount.Account, error) {
	var accounts []connectedaccount.Account
	for id, account := range a.storage.accounts {
		if account.userID != userID {
			continue
		}
		status := connectedaccount.StatusUsable
		switch {
		case account.sender.NeedsReconnect:
			status = connectedaccount.StatusNeedsReconnect
		case account.sender.Credential.KeyID == lostKeyID:
			status = connectedaccount.StatusTokenUnreadable
		}
		accounts = append(accounts, connectedaccount.Account{ID: id, UserID: userID, Email: account.sender.Email, Status: status})
	}
	return accounts, nil
}

// failingAccounts is [memoryAccounts] whose List fails while *fail is set.
type failingAccounts struct {
	memoryAccounts
	fail *bool
}

func (a failingAccounts) List(ctx context.Context, userID int64) ([]connectedaccount.Account, error) {
	if *a.fail {
		return nil, errors.New("accounts unavailable")
	}
	return a.memoryAccounts.List(ctx, userID)
}

// newTestService returns a service over storage whose clock reads *now.
func newTestService(storage *memoryStorage, now *time.Time) *Service {
	return NewService(storage, memoryAccounts{storage}, func() time.Time { return *now })
}

// testDraft returns a draft from account 5 with a new key.
func testDraft() Draft {
	return Draft{ConnectedAccountID: 5, IdempotencyKey: uuid.New(), Content: testContent()}
}

func TestCreate(t *testing.T) {
	storage := newMemoryStorage()
	now := createNow
	service := newTestService(storage, &now)
	content := testContent()
	content.To = []string{" Ann@Example.com "}

	message, err := service.Create(t.Context(), 3, Draft{ConnectedAccountID: 5, IdempotencyKey: uuid.New(), Content: content})

	require.NoError(t, err)
	assert.Equal(t, int64(5), message.ConnectedAccountID)
	assert.Equal(t, "me@gmail.com", message.From)
	assert.Equal(t, []string{"Ann@Example.com"}, message.To, "saved as parsed")
	assert.Equal(t, StatusWaiting, message.Status)
	assert.True(t, strings.HasSuffix(message.InbokMessageID, "@gmail.com>"), message.InbokMessageID)
	read, err := service.Get(t.Context(), 3, message.ID)
	require.NoError(t, err)
	assert.Equal(t, message, read)
}

func TestCreateOtherMembersAccount(t *testing.T) {
	storage := newMemoryStorage()
	now := createNow
	service := newTestService(storage, &now)

	_, err := service.Create(t.Context(), 3, Draft{ConnectedAccountID: 8, IdempotencyKey: uuid.New(), Content: testContent()})

	require.ErrorIs(t, err, ErrAccountNotFound)
	assert.Empty(t, storage.messages)
}

func TestCreateNeedsReconnect(t *testing.T) {
	storage := newMemoryStorage()
	now := createNow
	service := newTestService(storage, &now)

	_, err := service.Create(t.Context(), 3, Draft{ConnectedAccountID: 6, IdempotencyKey: uuid.New(), Content: testContent()})

	require.ErrorIs(t, err, ErrNeedsReconnect)
	assert.Empty(t, storage.messages)
}

// A token no key opens could only fail to send, so it is refused before
// the limit counts an attempt.
func TestCreateTokenUnreadable(t *testing.T) {
	storage := newMemoryStorage()
	now := createNow
	service := newTestService(storage, &now)

	_, err := service.Create(t.Context(), 3, Draft{ConnectedAccountID: 9, IdempotencyKey: uuid.New(), Content: testContent()})

	require.ErrorIs(t, err, ErrTokenUnreadable)
	assert.Empty(t, storage.messages)
	assert.Empty(t, storage.attempts[3])
}

// At the limit, the member still hears that the account needs reconnecting,
// which waiting would not fix.
func TestCreateTokenUnreadableAtLimit(t *testing.T) {
	storage := newMemoryStorage()
	now := createNow
	service := newTestService(storage, &now)
	fillAttempts(storage, 3, now, shortSendLimit.Max)

	_, err := service.Create(t.Context(), 3, Draft{ConnectedAccountID: 9, IdempotencyKey: uuid.New(), Content: testContent()})

	require.ErrorIs(t, err, ErrTokenUnreadable)
	require.NotErrorIs(t, err, ErrThrottled)
}

// A repeat of a request whose message was saved before the token became
// unreadable gets that message back: the key is checked before the account.
func TestCreateRepeatedTokenUnreadable(t *testing.T) {
	storage := newMemoryStorage()
	now := createNow
	service := newTestService(storage, &now)
	draft := testDraft()
	first, err := service.Create(t.Context(), 3, draft)
	require.NoError(t, err)
	account := storage.accounts[5]
	account.sender.Credential = connectedaccount.Credential{KeyID: lostKeyID}
	storage.accounts[5] = account

	again, err := service.Create(t.Context(), 3, draft)

	require.NoError(t, err)
	assert.Equal(t, first.ID, again.ID)
	assert.Equal(t, connectedaccount.StatusTokenUnreadable, again.Account.Status, "the account as read now")
	assert.Len(t, storage.messages, 1)
}

func TestCreateInvalidContent(t *testing.T) {
	storage := newMemoryStorage()
	now := createNow
	service := newTestService(storage, &now)
	content := testContent()
	content.Subject = "Hi\nthere"

	_, err := service.Create(t.Context(), 3, Draft{ConnectedAccountID: 5, IdempotencyKey: uuid.New(), Content: content})

	require.ErrorIs(t, err, ErrSubjectInvalid)
	assert.Zero(t, storage.buildCalls, "refused before storage locks anything")
}

func TestGetOtherMembersMessage(t *testing.T) {
	storage := newMemoryStorage()
	now := createNow
	service := newTestService(storage, &now)
	message, err := service.Create(t.Context(), 3, testDraft())
	require.NoError(t, err)

	_, err = service.Get(t.Context(), 4, message.ID)

	require.ErrorIs(t, err, ErrNotFound)
}

func TestCreateDeactivated(t *testing.T) {
	storage := newMemoryStorage()
	storage.deactivated = true
	now := createNow

	_, err := newTestService(storage, &now).Create(t.Context(), 3, testDraft())

	require.ErrorIs(t, err, ErrDeactivated)
}

// A repeat of a key with the same draft returns the first message and saves
// and counts nothing more, even once the member is at the limit.
func TestCreateRepeated(t *testing.T) {
	storage := newMemoryStorage()
	now := createNow
	service := newTestService(storage, &now)
	draft := testDraft()
	draft.To = []string{" Ann@Example.com "}
	first, err := service.Create(t.Context(), 3, draft)
	require.NoError(t, err)
	fillAttempts(storage, 3, now, shortSendLimit.Max)

	again, err := service.Create(t.Context(), 3, draft)

	require.NoError(t, err)
	assert.Equal(t, first, again)
	assert.Len(t, storage.messages, 1)
	assert.Len(t, storage.attempts[3], shortSendLimit.Max+1, "the repeat counts no attempt")
}

// A key that saved another of the member's messages is refused, whatever
// differs.
func TestCreateKeyReused(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Draft)
	}{
		{"other recipient", func(d *Draft) { d.Cc = []string{"cc@example.com"} }},
		{"other subject", func(d *Draft) { d.Subject += "!" }},
		{"other body", func(d *Draft) { d.Body += "!" }},
		{"other account", func(d *Draft) { d.ConnectedAccountID = 7 }},
		{"recipients reordered", func(d *Draft) { d.To = []string{"bob@example.org", "ann@example.com"} }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			storage := newMemoryStorage()
			storage.accounts[7] = memoryAccount{userID: 3, sender: Sender{ConnectedAccountID: 7, Email: "work@gmail.com"}}
			now := createNow
			service := newTestService(storage, &now)
			draft := testDraft()
			draft.To = []string{"ann@example.com", "bob@example.org"}
			_, err := service.Create(t.Context(), 3, draft)
			require.NoError(t, err)
			repeat := draft
			repeat.Content.To = slices.Clone(draft.To)
			c.change(&repeat)

			_, err = service.Create(t.Context(), 3, repeat)

			require.ErrorIs(t, err, ErrKeyReused)
			assert.Len(t, storage.messages, 1)
		})
	}
}

// A key is the member's own: another member's request with the same key is
// another request, and saves its own message.
func TestCreateKeyPerMember(t *testing.T) {
	storage := newMemoryStorage()
	now := createNow
	service := newTestService(storage, &now)
	draft := testDraft()
	mine, err := service.Create(t.Context(), 3, draft)
	require.NoError(t, err)
	draft.ConnectedAccountID = 8

	theirs, err := service.Create(t.Context(), 4, draft)

	require.NoError(t, err)
	assert.NotEqual(t, mine.ID, theirs.ID)
	assert.Len(t, storage.messages, 2)
}

// fillAttempts counts n attempts for userID at at.
func fillAttempts(storage *memoryStorage, userID int64, at time.Time, n int) {
	for range n {
		storage.attempts[userID] = append(storage.attempts[userID], at)
	}
}

// Each window refuses one more attempt, saying how long until one fits.
func TestCreateThrottled(t *testing.T) {
	cases := []struct {
		name  string
		limit attemptlimit.Limit
		// age is how old the earliest attempt is.
		age  time.Duration
		wait time.Duration
	}{
		{"short window", shortSendLimit, 5 * time.Minute, 10 * time.Minute},
		{"long window", longSendLimit, 2 * time.Hour, 22 * time.Hour},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			storage := newMemoryStorage()
			now := createNow
			service := newTestService(storage, &now)
			fillAttempts(storage, 3, now.Add(-c.age), c.limit.Max)

			_, err := service.Create(t.Context(), 3, testDraft())

			require.ErrorIs(t, err, ErrThrottled)
			refusal, ok := errors.AsType[*attemptlimit.Error](err)
			require.True(t, ok)
			assert.Equal(t, c.wait, refusal.Wait)
			assert.Empty(t, storage.messages)
		})
	}
}

// The limit is the member's, counted across their accounts, not another
// member's.
func TestCreateLimitPerMember(t *testing.T) {
	storage := newMemoryStorage()
	storage.accounts[7] = memoryAccount{userID: 3, sender: Sender{ConnectedAccountID: 7, Email: "work@gmail.com"}}
	now := createNow
	service := newTestService(storage, &now)
	fillAttempts(storage, 4, now, shortSendLimit.Max)
	for range shortSendLimit.Max - 1 {
		_, err := service.Create(t.Context(), 3, testDraft())
		require.NoError(t, err)
	}
	draft := testDraft()
	draft.ConnectedAccountID = 7
	_, err := service.Create(t.Context(), 3, draft)
	require.NoError(t, err, "member 4's attempts do not count")
	draft.IdempotencyKey = uuid.New()

	_, err = service.Create(t.Context(), 3, draft)

	require.ErrorIs(t, err, ErrThrottled)
}

// sendAgainTest is a member's message that ended with status.
func sendAgainTest(t *testing.T, status Status) (*memoryStorage, *Service, Message) {
	t.Helper()
	storage := newMemoryStorage()
	now := createNow
	service := newTestService(storage, &now)
	_, err := service.Create(t.Context(), 3, testDraft())
	require.NoError(t, err)
	storage.messages[0].Status = status
	if status == StatusFailed {
		storage.messages[0].Failure = FailureSenderChanged
	}
	return storage, service, storage.messages[0]
}

// A copy has the original's content, a new ID and Message-ID, and the
// account's address now, e.g. after a send found it changed.
func TestSendAgain(t *testing.T) {
	for _, status := range []Status{StatusFailed, StatusUnknown} {
		t.Run(string(status), func(t *testing.T) {
			storage, service, original := sendAgainTest(t, status)
			account := storage.accounts[5]
			account.sender.Email = "renamed@gmail.com"
			storage.accounts[5] = account

			copied, err := service.SendAgain(t.Context(), 3, original.ID, nil)

			require.NoError(t, err)
			assert.NotEqual(t, original.ID, copied.ID)
			assert.NotEqual(t, original.InbokMessageID, copied.InbokMessageID)
			assert.Equal(t, original.Content, copied.Content)
			assert.Equal(t, int64(5), copied.ConnectedAccountID)
			assert.Equal(t, "renamed@gmail.com", copied.From)
			assert.True(t, strings.HasSuffix(copied.InbokMessageID, "@gmail.com>"), copied.InbokMessageID)
			assert.Equal(t, StatusWaiting, copied.Status)
			read, err := service.Get(t.Context(), 3, original.ID)
			require.NoError(t, err)
			assert.Equal(t, &copied.ID, read.SentAgainAs)
		})
	}
}

// Sending a message again twice returns the one copy, counting one attempt.
func TestSendAgainTwice(t *testing.T) {
	storage, service, original := sendAgainTest(t, StatusUnknown)
	first, err := service.SendAgain(t.Context(), 3, original.ID, nil)
	require.NoError(t, err)

	second, err := service.SendAgain(t.Context(), 3, original.ID, nil)

	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Len(t, storage.messages, 2)
	assert.Len(t, storage.attempts[3], 2, "the create and one copy")
}

// Edits are the copy's content, parsed as a new message's; the original
// then names the copy, as without edits.
func TestSendAgainWithEdits(t *testing.T) {
	storage, service, original := sendAgainTest(t, StatusFailed)
	edits := Content{To: []string{"ann@example.com"}, Cc: []string{}, Bcc: []string{}, Subject: "Lunch, fixed", Body: "Friday?"}

	copied, err := service.SendAgain(t.Context(), 3, original.ID, &edits)

	require.NoError(t, err)
	assert.Equal(t, edits, copied.Content)
	assert.Equal(t, int64(5), copied.ConnectedAccountID)
	assert.Equal(t, &copied.ID, storage.messages[0].SentAgainAs)
}

// Edits that break a new message's rules are refused before anything is
// read or saved.
func TestSendAgainInvalidEdits(t *testing.T) {
	storage, service, original := sendAgainTest(t, StatusFailed)
	edits := Content{To: []string{}, Cc: []string{}, Bcc: []string{}, Subject: "Lunch", Body: "Friday?"}

	_, err := service.SendAgain(t.Context(), 3, original.ID, &edits)

	require.ErrorIs(t, err, ErrNoRecipients)
	assert.Len(t, storage.messages, 1)
	assert.Len(t, storage.attempts[3], 1, "the create only")
}

// A repeat of an edited send again returns its copy; other edits, which
// that copy doesn't hold, are refused, and no edits return the copy.
func TestSendAgainWithEditsTwice(t *testing.T) {
	storage, service, original := sendAgainTest(t, StatusFailed)
	edits := Content{To: []string{"ann@example.com"}, Cc: []string{}, Bcc: []string{}, Subject: "Lunch", Body: "Friday?"}
	first, err := service.SendAgain(t.Context(), 3, original.ID, &edits)
	require.NoError(t, err)

	repeat, err := service.SendAgain(t.Context(), 3, original.ID, &edits)
	require.NoError(t, err)
	assert.Equal(t, first, repeat)

	other := edits
	other.Body = "Saturday?"
	_, err = service.SendAgain(t.Context(), 3, original.ID, &other)
	require.ErrorIs(t, err, ErrSentAgainOtherwise)

	plain, err := service.SendAgain(t.Context(), 3, original.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, first, plain)
	assert.Len(t, storage.messages, 2)
}

func TestSendAgainRefused(t *testing.T) {
	for _, status := range []Status{StatusWaiting, StatusSending, StatusSent} {
		t.Run(string(status), func(t *testing.T) {
			storage, service, original := sendAgainTest(t, status)

			_, err := service.SendAgain(t.Context(), 3, original.ID, nil)

			require.ErrorIs(t, err, ErrNotSendableAgain)
			assert.Len(t, storage.messages, 1)
		})
	}
}

func TestSendAgainNeedsReconnect(t *testing.T) {
	storage, service, original := sendAgainTest(t, StatusFailed)
	account := storage.accounts[5]
	account.sender.NeedsReconnect = true
	storage.accounts[5] = account

	_, err := service.SendAgain(t.Context(), 3, original.ID, nil)

	require.ErrorIs(t, err, ErrNeedsReconnect)
}

func TestSendAgainTokenUnreadable(t *testing.T) {
	storage, service, original := sendAgainTest(t, StatusFailed)
	account := storage.accounts[5]
	account.sender.Credential = connectedaccount.Credential{KeyID: lostKeyID}
	storage.accounts[5] = account

	_, err := service.SendAgain(t.Context(), 3, original.ID, nil)

	require.ErrorIs(t, err, ErrTokenUnreadable)
	assert.Nil(t, storage.messages[0].SentAgainAs)
}

func TestSendAgainThrottled(t *testing.T) {
	storage, service, original := sendAgainTest(t, StatusFailed)
	fillAttempts(storage, 3, createNow, shortSendLimit.Max)

	_, err := service.SendAgain(t.Context(), 3, original.ID, nil)

	require.ErrorIs(t, err, ErrThrottled)
	assert.Nil(t, storage.messages[0].SentAgainAs)
}

func TestSendAgainOtherMembers(t *testing.T) {
	_, service, original := sendAgainTest(t, StatusFailed)

	_, err := service.SendAgain(t.Context(), 4, original.ID, nil)

	require.ErrorIs(t, err, ErrNotFound)
}

// A message is outstanding while it is being sent, and once it ended
// unsent until it is sent again.
func TestOutstanding(t *testing.T) {
	copyID := uuid.New()
	tests := []struct {
		name    string
		message Message
		want    bool
	}{
		{"waiting", Message{Status: StatusWaiting}, true},
		{"sending", Message{Status: StatusSending}, true},
		{"sent", Message{Status: StatusSent}, false},
		{"failed", Message{Status: StatusFailed, Failure: FailureRefused}, true},
		{"unknown", Message{Status: StatusUnknown}, true},
		{"failed, sent again", Message{Status: StatusFailed, Failure: FailureRefused, SentAgainAs: &copyID}, false},
		{"unknown, sent again", Message{Status: StatusUnknown, SentAgainAs: &copyID}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, test.message.Outstanding())
		})
	}
}

func TestAllowedActions(t *testing.T) {
	copyID := uuid.New()
	usable := connectedaccount.Account{Status: connectedaccount.StatusUsable}
	stale := connectedaccount.Account{Status: connectedaccount.StatusNeedsReconnect}
	unreadable := connectedaccount.Account{Status: connectedaccount.StatusTokenUnreadable}
	tests := []struct {
		name    string
		message Message
		offered bool
		want    []Action
	}{
		{"waiting", Message{Status: StatusWaiting, Account: usable}, true, nil},
		{"sending", Message{Status: StatusSending, Account: usable}, true, nil},
		{"sent", Message{Status: StatusSent, Account: usable}, true, nil},
		{"failed", Message{Status: StatusFailed, Failure: FailureRefused, Account: usable}, true, []Action{ActionSendAgain}},
		{"unknown", Message{Status: StatusUnknown, Account: usable}, true, []Action{ActionSendAgain}},
		{"Gmail not set up", Message{Status: StatusFailed, Failure: FailureGaveUp, Account: usable}, false, nil},
		{"account needs reconnecting", Message{Status: StatusFailed, Failure: FailureNeedsReconnect, Account: stale}, true, nil},
		{"account's token unreadable", Message{Status: StatusFailed, Failure: FailureTokenUnreadable, Account: unreadable}, true, nil},
		{"account disconnected", Message{Status: StatusUnknown}, true, nil},
		{"sent again", Message{Status: StatusUnknown, SentAgainAs: &copyID, Account: usable}, true, []Action{ActionSendAgain}},
		{"sent again, account needs reconnecting", Message{Status: StatusFailed, SentAgainAs: &copyID, Account: stale}, true, []Action{ActionSendAgain}},
		{"sent again, Gmail not set up", Message{Status: StatusFailed, SentAgainAs: &copyID, Account: usable}, false, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, test.message.AllowedActions(test.offered))
		})
	}
}

// What a read lists is what Send again accepts with Gmail set up: for
// every status, with the account usable, needing reconnecting, or holding a
// token no key opens.
func TestAllowedActionsMatchSendAgain(t *testing.T) {
	accounts := map[string]func(*Sender){
		"usable":           func(*Sender) {},
		"needs reconnect":  func(sender *Sender) { sender.NeedsReconnect = true },
		"token unreadable": func(sender *Sender) { sender.Credential = connectedaccount.Credential{KeyID: lostKeyID} },
	}
	for _, status := range []Status{StatusWaiting, StatusSending, StatusSent, StatusFailed, StatusUnknown} {
		for name, change := range accounts {
			t.Run(fmt.Sprintf("%s, account %s", status, name), func(t *testing.T) {
				storage, service, original := sendAgainTest(t, status)
				account := storage.accounts[5]
				change(&account.sender)
				storage.accounts[5] = account
				read, err := service.Get(t.Context(), 3, original.ID)
				require.NoError(t, err)

				_, err = service.SendAgain(t.Context(), 3, original.ID, nil)

				listed := slices.Contains(read.AllowedActions(true), ActionSendAgain)
				assert.Equal(t, listed, err == nil, "listed %t, Send again: %v", listed, err)
			})
		}
	}
}

// A message sent again lists Send again after its account stops working,
// as Send again still returns its copy.
func TestAllowedActionsSentAgainNeedsReconnect(t *testing.T) {
	storage, service, original := sendAgainTest(t, StatusFailed)
	copied, err := service.SendAgain(t.Context(), 3, original.ID, nil)
	require.NoError(t, err)
	account := storage.accounts[5]
	account.sender.NeedsReconnect = true
	storage.accounts[5] = account
	read, err := service.Get(t.Context(), 3, original.ID)
	require.NoError(t, err)

	again, err := service.SendAgain(t.Context(), 3, original.ID, nil)

	assert.Equal(t, []Action{ActionSendAgain}, read.AllowedActions(true))
	require.NoError(t, err)
	assert.Equal(t, copied.ID, again.ID)
}

// Every message the service returns carries its sending account as read
// now, which its allowed actions need.
func TestMessagesCarryTheirAccount(t *testing.T) {
	storage := newMemoryStorage()
	now := createNow
	service := newTestService(storage, &now)

	created, err := service.Create(t.Context(), 3, testDraft())
	require.NoError(t, err)
	account := storage.accounts[5]
	account.sender.NeedsReconnect = true
	storage.accounts[5] = account
	read, err := service.Get(t.Context(), 3, created.ID)
	require.NoError(t, err)
	listed, err := service.List(t.Context(), 3)
	require.NoError(t, err)

	assert.Equal(t, connectedaccount.StatusUsable, created.Account.Status)
	assert.Equal(t, connectedaccount.StatusNeedsReconnect, read.Account.Status)
	require.Len(t, listed, 1)
	assert.Equal(t, connectedaccount.StatusNeedsReconnect, listed[0].Account.Status)
}

// A write whose account read fails after saving answers the error, and its
// retry returns what the first saved, saving nothing more: Create and Reply
// by their key, Send again by the original's link to its copy.
func TestWriteRetriedAfterAccountsFail(t *testing.T) {
	for name, write := range map[string]func(*Service, Message) (Message, error){
		"create": func(service *Service, _ Message) (Message, error) {
			return service.Create(t.Context(), 3, Draft{ConnectedAccountID: 5, IdempotencyKey: lunchKey, Content: testContent()})
		},
		"reply": func(service *Service, _ Message) (Message, error) {
			reply := testReply()
			reply.IdempotencyKey = lunchKey
			return service.Reply(t.Context(), 3, reply)
		},
		"send again": func(service *Service, original Message) (Message, error) {
			return service.SendAgain(t.Context(), 3, original.ID, nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			storage := newMemoryStorage()
			storage.answered[answeredID] = testAnswered()
			fail := false
			service := NewService(storage, failingAccounts{memoryAccounts{storage}, &fail}, func() time.Time { return createNow })
			original, err := service.Create(t.Context(), 3, testDraft())
			require.NoError(t, err)
			storage.messages[0].Status = StatusFailed
			saved := len(storage.messages)
			fail = true

			_, err = write(service, original)
			require.Error(t, err)
			require.Len(t, storage.messages, saved+1, "the failed try saved")
			fail = false
			again, err := write(service, original)

			require.NoError(t, err)
			assert.Len(t, storage.messages, saved+1, "saved once")
			assert.Equal(t, storage.messages[saved].ID, again.ID)
			assert.Len(t, storage.attempts[3], saved+1, "one attempt per message")
			assert.Equal(t, connectedaccount.StatusUsable, again.Account.Status)
		})
	}
}

// lunchKey is the idempotency key a retried write repeats.
var lunchKey = uuid.MustParse("0a1b2c3d-0000-4000-8000-0000000000aa")

// The list holds the member's newest messages only.
func TestList(t *testing.T) {
	storage := newMemoryStorage()
	now := createNow
	service := newTestService(storage, &now)
	for range maxListed + 1 {
		_, err := service.Create(t.Context(), 3, testDraft())
		require.NoError(t, err)
		now = now.Add(time.Hour)
	}
	draft := testDraft()
	draft.ConnectedAccountID = 8
	_, err := service.Create(t.Context(), 4, draft)
	require.NoError(t, err)

	listed, err := service.List(t.Context(), 3)

	require.NoError(t, err)
	require.Len(t, listed, maxListed)
	assert.Equal(t, storage.messages[maxListed].ID, listed[0].ID, "newest first")
	assert.Equal(t, storage.messages[1].ID, listed[maxListed-1].ID, "the oldest left out")
}

// disconnect deletes accountID's messages and their keys, as a disconnect
// does.
func (m *memoryStorage) disconnect(accountID int64) {
	m.messages = slices.DeleteFunc(m.messages, func(message Message) bool {
		if message.ConnectedAccountID != accountID {
			return false
		}
		maps.DeleteFunc(m.keys, func(_ memoryKey, keyed memoryKeyed) bool { return keyed.messageID == message.ID })
		return true
	})
}

// A key whose message a disconnect deletes between finding the key and
// reading the message is free again: the request is saved as a new one, not
// refused for an account it may not name.
func TestCreateKeyFreed(t *testing.T) {
	storage := newMemoryStorage()
	storage.accounts[7] = memoryAccount{userID: 3, sender: Sender{ConnectedAccountID: 7, Email: "work@gmail.com"}}
	now := createNow
	service := newTestService(storage, &now)
	draft := testDraft()
	_, err := service.Create(t.Context(), 3, draft)
	require.NoError(t, err)
	storage.beforeKeyRead = func() {
		storage.beforeKeyRead = nil
		storage.disconnect(5)
	}
	draft.ConnectedAccountID = 7

	message, err := service.Create(t.Context(), 3, draft)

	require.NoError(t, err)
	assert.Equal(t, int64(7), message.ConnectedAccountID)
	assert.Len(t, storage.messages, 1)
}

// A key freed every time ends with an error rather than looping.
func TestCreateKeyFreedTwice(t *testing.T) {
	storage := newMemoryStorage()
	now := createNow
	service := newTestService(storage, &now)
	draft := testDraft()
	_, err := service.Create(t.Context(), 3, draft)
	require.NoError(t, err)
	reads := 0
	storage.beforeKeyRead = func() {
		reads++
		storage.disconnect(5)
		// The member saves the key again from the same account.
		storage.keys[memoryKey{3, draft.IdempotencyKey}] = memoryKeyed{messageID: uuid.New()}
	}

	_, err = service.Create(t.Context(), 3, draft)

	require.ErrorIs(t, err, errKeyFreed)
	assert.Equal(t, 2, reads)
}
