package outgoingmessage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/attemptlimit"
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
	// attempts are every member's send attempts, by member.
	attempts map[int64][]time.Time
	// buildCalls counts calls to build, which run under the account's lock.
	buildCalls int
	// deactivated is whether every account's member is deactivated.
	deactivated bool
}

func newMemoryStorage() *memoryStorage {
	return &memoryStorage{
		accounts: map[int64]memoryAccount{
			5: {userID: 3, sender: Sender{ConnectedAccountID: 5, Email: "me@gmail.com"}},
			6: {userID: 3, sender: Sender{ConnectedAccountID: 6, Email: "stale@gmail.com", NeedsReconnect: true}},
			8: {userID: 4, sender: Sender{ConnectedAccountID: 8, Email: "other@gmail.com"}},
		},
		attempts: map[int64][]time.Time{},
	}
}

func (m *memoryStorage) InsertOutgoingMessage(_ context.Context, attempt Attempt, accountID int64, build func(CreateState) (NewMessage, error)) (Message, error) {
	if m.deactivated {
		return Message{}, ErrDeactivated
	}
	account, ok := m.accounts[accountID]
	if !ok || account.userID != attempt.UserID {
		return Message{}, ErrAccountNotFound
	}
	m.buildCalls++
	next, err := build(CreateState{Sender: account.sender, Attempts: m.attemptsSince(attempt)})
	if err != nil {
		return Message{}, err
	}
	return m.save(attempt, accountID, next), nil
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

// createNow is the clock of every create test.
var createNow = time.Date(2026, 11, 9, 10, 30, 0, 0, time.UTC)

// newTestService returns a service over storage whose clock reads *now.
func newTestService(storage *memoryStorage, now *time.Time) *Service {
	return NewService(storage, func() time.Time { return *now })
}

// testDraft returns a draft from account 5.
func testDraft() Draft {
	return Draft{ConnectedAccountID: 5, Content: testContent()}
}

func TestCreate(t *testing.T) {
	storage := newMemoryStorage()
	now := createNow
	service := newTestService(storage, &now)
	content := testContent()
	content.To = []string{" Ann@Example.com "}

	message, err := service.Create(t.Context(), 3, Draft{ConnectedAccountID: 5, Content: content})

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

	_, err := service.Create(t.Context(), 3, Draft{ConnectedAccountID: 8, Content: testContent()})

	require.ErrorIs(t, err, ErrAccountNotFound)
	assert.Empty(t, storage.messages)
}

func TestCreateNeedsReconnect(t *testing.T) {
	storage := newMemoryStorage()
	now := createNow
	service := newTestService(storage, &now)

	_, err := service.Create(t.Context(), 3, Draft{ConnectedAccountID: 6, Content: testContent()})

	require.ErrorIs(t, err, ErrNeedsReconnect)
	assert.Empty(t, storage.messages)
}

func TestCreateInvalidContent(t *testing.T) {
	storage := newMemoryStorage()
	now := createNow
	service := newTestService(storage, &now)
	content := testContent()
	content.Subject = "Hi\nthere"

	_, err := service.Create(t.Context(), 3, Draft{ConnectedAccountID: 5, Content: content})

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

	_, err = service.Create(t.Context(), 3, draft)

	require.ErrorIs(t, err, ErrThrottled)
}
