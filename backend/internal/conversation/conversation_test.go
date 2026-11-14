package conversation

import (
	"context"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/mailsync"
)

func TestParsePlace(t *testing.T) {
	for _, view := range Views {
		place, err := ParsePlace(string(view))
		require.NoError(t, err)

		assert.Equal(t, Place{View: view}, place)
		assert.Equal(t, string(view), place.String())
	}
}

func TestParsePlaceMailbox(t *testing.T) {
	label := uuid.New()

	place, err := ParsePlace(label.String())

	require.NoError(t, err)
	assert.Equal(t, Place{Mailbox: label}, place)
	assert.Equal(t, label.String(), place.String())
}

func TestParsePlaceInvalid(t *testing.T) {
	for _, text := range []string{"", "Inbox", "drafts", "label", uuid.Nil().String()} {
		_, err := ParsePlace(text)

		require.ErrorIs(t, err, ErrInvalidPlace, text)
	}
}

// fakeStorage records the query it was asked and answers with rows.
type fakeStorage struct {
	Storage
	asked    *Query
	rows     []Conversation
	messages []Message
}

func (f *fakeStorage) ListConversations(_ context.Context, _ int64, query Query) ([]Conversation, error) {
	f.asked = &query
	return f.rows, nil
}

func (f *fakeStorage) ReadConversation(context.Context, int64, uuid.UUID) (Conversation, []Message, error) {
	return f.rows[0], f.messages, nil
}

func TestListLimit(t *testing.T) {
	storage := &fakeStorage{rows: []Conversation{{ID: uuid.New()}}}
	service := NewService(storage)
	query := Query{Place: Place{View: ViewInbox}, Limit: MaxLimit, Before: &Cursor{Date: time.Now(), ID: uuid.New()}}

	rows, err := service.List(t.Context(), 1, query)

	require.NoError(t, err)
	assert.Equal(t, storage.rows, rows)
	assert.Equal(t, &query, storage.asked)
}

func TestListLimitOutOfRange(t *testing.T) {
	for _, limit := range []int{0, -1, MaxLimit + 1} {
		storage := &fakeStorage{}

		_, err := NewService(storage).List(t.Context(), 1, Query{Place: Place{View: ViewInbox}, Limit: limit})

		require.ErrorIs(t, err, ErrInvalidLimit)
		assert.Nil(t, storage.asked, "nothing is read")
	}
}

func TestGetLeavesOutDrafts(t *testing.T) {
	received, reply := message(1, "asha", inbox), message(2, "member", drafts)
	storage := &fakeStorage{rows: []Conversation{{ID: uuid.New()}}, messages: []Message{received, reply}}

	_, messages, err := NewService(storage).Get(t.Context(), 1, storage.rows[0].ID)

	require.NoError(t, err)
	assert.Equal(t, []Message{received}, messages)
}

// A draft shows nowhere, so nothing links to it: drafts go first.
func TestGetLinksNoAnswerToADraft(t *testing.T) {
	draft := message(1, "member", drafts)
	draft.MessageID = "<draft@x>"
	reply := message(2, "asha", inbox)
	reply.InReplyTo = []string{"<draft@x>"}
	storage := &fakeStorage{rows: []Conversation{{ID: uuid.New()}}, messages: []Message{draft, reply}}

	_, messages, err := NewService(storage).Get(t.Context(), 1, storage.rows[0].ID)

	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, uuid.Nil(), messages[0].Answers)
}

func TestGetLinksAnswers(t *testing.T) {
	// Given a message, a reply to it, a reply to a message inbok lacks, a
	// reply naming its parents with the one answered last, two copies of
	// one Message-ID, a reply to those, and a message naming itself.
	ids := make([]uuid.UUID, 7)
	for i := range ids {
		ids[i] = uuid.New()
	}
	messages := []Message{
		{ID: ids[0], MessageID: "<a@x>"},
		{ID: ids[1], MessageID: "<b@x>", InReplyTo: []string{"<a@x>"}},
		{ID: ids[2], MessageID: "<c@x>", InReplyTo: []string{"<gone@x>"}},
		{ID: ids[3], MessageID: "<d@x>", InReplyTo: []string{"<a@x>", "<b@x>"}},
		{ID: ids[4], MessageID: "<d@x>"},
		{ID: ids[5], InReplyTo: []string{"<d@x>"}},
		{ID: ids[6], MessageID: "<self@x>", InReplyTo: []string{"<self@x>"}},
	}
	storage := &fakeStorage{rows: []Conversation{{ID: uuid.New()}}, messages: messages}

	_, got, err := NewService(storage).Get(t.Context(), 1, storage.rows[0].ID)

	// Then each answers the message with the last ID it names, the first
	// copy of two, and none for one inbok lacks or itself.
	require.NoError(t, err)
	answers := make([]uuid.UUID, len(got))
	for i, m := range got {
		answers[i] = m.Answers
	}
	none := uuid.Nil()
	assert.Equal(t, []uuid.UUID{none, ids[0], none, ids[1], none, ids[3], none}, answers)
}

func TestMessagePlaces(t *testing.T) {
	gmails := Mailbox{ID: uuid.New(), MadeBy: mailsync.MadeByProvider}
	labelled := message(1, "asha", inbox, receipts, gmails)
	trashed := message(2, "blair", trash)
	junk := message(3, "casey", spam)
	reply := message(4, "member", sent)

	assert.Equal(t, []uuid.UUID{receipts.ID}, labelled.LabelIDs(), "only the member's own mailboxes with no role")
	assert.Equal(t, []uuid.UUID{}, trashed.LabelIDs())
	assert.Equal(t, []bool{false, false}, []bool{labelled.InSpam(), labelled.InTrash()})
	assert.Equal(t, []bool{false, true}, []bool{trashed.InSpam(), trashed.InTrash()})
	assert.Equal(t, []bool{true, false}, []bool{junk.InSpam(), junk.InTrash()})
	assert.False(t, labelled.InSent())
	assert.True(t, reply.InSent(), "its account sent it")
}
