package outgoingmessage

import (
	"context"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	// buildCalls counts calls to build, which run under the account's lock.
	buildCalls int
	// deactivated is whether every account's member is deactivated.
	deactivated bool
}

func newMemoryStorage() *memoryStorage {
	return &memoryStorage{accounts: map[int64]memoryAccount{
		5: {userID: 3, sender: Sender{ConnectedAccountID: 5, Email: "me@gmail.com"}},
		6: {userID: 3, sender: Sender{ConnectedAccountID: 6, Email: "stale@gmail.com", NeedsReconnect: true}},
		8: {userID: 4, sender: Sender{ConnectedAccountID: 8, Email: "other@gmail.com"}},
	}}
}

func (m *memoryStorage) InsertOutgoingMessage(_ context.Context, userID, accountID int64, build func(Sender) (NewMessage, error)) (Message, error) {
	account, ok := m.accounts[accountID]
	if !ok || account.userID != userID {
		return Message{}, ErrAccountNotFound
	}
	m.buildCalls++
	next, err := build(account.sender)
	if err != nil {
		return Message{}, err
	}
	saved := Message{
		ID:                 uuid.New(),
		ConnectedAccountID: accountID,
		From:               next.From,
		Content:            next.Content,
		InbokMessageID:     next.InbokMessageID,
		Status:             StatusWaiting,
		CreatedAt:          time.Date(2026, 11, 9, 10, 30, 0, 0, time.UTC),
	}
	m.messages = append(m.messages, saved)
	return saved, nil
}

func (m *memoryStorage) ReadOutgoingMessage(_ context.Context, userID int64, messageID uuid.UUID) (Message, error) {
	for _, message := range m.messages {
		if message.ID == messageID && m.accounts[message.ConnectedAccountID].userID == userID {
			return message, nil
		}
	}
	return Message{}, ErrNotFound
}

func TestCreate(t *testing.T) {
	storage := newMemoryStorage()
	service := NewService(storage)
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
	service := NewService(storage)

	_, err := service.Create(t.Context(), 3, Draft{ConnectedAccountID: 8, Content: testContent()})

	require.ErrorIs(t, err, ErrAccountNotFound)
	assert.Empty(t, storage.messages)
}

func TestCreateNeedsReconnect(t *testing.T) {
	storage := newMemoryStorage()
	service := NewService(storage)

	_, err := service.Create(t.Context(), 3, Draft{ConnectedAccountID: 6, Content: testContent()})

	require.ErrorIs(t, err, ErrNeedsReconnect)
	assert.Empty(t, storage.messages)
}

func TestCreateInvalidContent(t *testing.T) {
	storage := newMemoryStorage()
	service := NewService(storage)
	content := testContent()
	content.Subject = "Hi\nthere"

	_, err := service.Create(t.Context(), 3, Draft{ConnectedAccountID: 5, Content: content})

	require.ErrorIs(t, err, ErrSubjectInvalid)
	assert.Zero(t, storage.buildCalls, "refused before storage locks anything")
}

func TestGetOtherMembersMessage(t *testing.T) {
	storage := newMemoryStorage()
	service := NewService(storage)
	message, err := service.Create(t.Context(), 3, Draft{ConnectedAccountID: 5, Content: testContent()})
	require.NoError(t, err)

	_, err = service.Get(t.Context(), 4, message.ID)

	require.ErrorIs(t, err, ErrNotFound)
}
