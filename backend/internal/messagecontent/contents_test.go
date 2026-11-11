package messagecontent_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/messagecontent"
	"inbok/internal/messagecontent/messagecontenttest"
)

// partsMemory keeps part trees in memory, by message. A save fails with
// saveErr when set.
type partsMemory struct {
	mu       sync.Mutex
	messages map[uuid.UUID]int64 // the messages there are, and their accounts
	trees    map[uuid.UUID]messagecontent.Part
	saveErr  error
}

// newPartsMemory returns storage holding messageIDs of account, none with
// parts.
func newPartsMemory(account int64, messageIDs ...uuid.UUID) *partsMemory {
	m := &partsMemory{messages: map[uuid.UUID]int64{}, trees: map[uuid.UUID]messagecontent.Part{}}
	for _, id := range messageIDs {
		m.messages[id] = account
	}
	return m
}

func (m *partsMemory) SaveMessageParts(_ context.Context, accountID int64, messageID uuid.UUID, root messagecontent.Part) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveErr != nil {
		return false, m.saveErr
	}
	if account, ok := m.messages[messageID]; !ok || account != accountID {
		return false, messagecontent.ErrMessageNotFound
	}
	if _, ok := m.trees[messageID]; ok {
		return false, nil
	}
	m.trees[messageID] = root
	return true, nil
}

func (m *partsMemory) MessageParts(_ context.Context, accountID int64, messageID uuid.UUID) (messagecontent.Part, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if account, ok := m.messages[messageID]; !ok || account != accountID {
		return messagecontent.Part{}, false, nil
	}
	root, ok := m.trees[messageID]
	return root, ok, nil
}

// flatten returns root and every part under it, depth first.
func flatten(root messagecontent.Part) []messagecontent.Part {
	parts := []messagecontent.Part{root}
	for _, child := range root.Parts {
		parts = append(parts, flatten(child)...)
	}
	return parts
}

// newsletter is a message's part tree: plain text and HTML alternatives,
// the HTML related to a logo, and a PDF.
func newsletter() messagecontent.Part {
	return messagecontent.Part{ID: "", MediaType: "multipart/mixed", Kind: messagecontent.KindMultipart, Parts: []messagecontent.Part{
		{ID: "0", MediaType: "multipart/alternative", Kind: messagecontent.KindMultipart, Parts: []messagecontent.Part{
			{ID: "0.0", MediaType: "text/plain", Charset: "utf-8", Kind: messagecontent.KindText},
			{ID: "0.1", MediaType: "multipart/related", Kind: messagecontent.KindMultipart, Parts: []messagecontent.Part{
				{ID: "0.1.0", MediaType: "text/html", Charset: "utf-8", Kind: messagecontent.KindText},
				{ID: "0.1.1", MediaType: "image/png", ContentID: "logo", Kind: messagecontent.KindInline},
			}},
		}},
		{ID: "1", MediaType: "application/pdf", Filename: "report.pdf", Kind: messagecontent.KindAttachment},
	}}
}

// newsletterTexts is the content of [newsletter]'s text parts.
var newsletterTexts = []messagecontent.Text{
	{PartID: "0.0", Content: []byte("Hello\r\n")},
	{PartID: "0.1.0", Content: []byte("<p>Hello</p>")},
}

// stored returns the content store holds under ref, failing the test when
// it holds none.
func stored(t *testing.T, store messagecontent.Store, ref messagecontent.Ref) string {
	t.Helper()
	r, err := store.Get(t.Context(), ref)
	require.NoError(t, err)
	defer r.Close()
	b, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(b)
}

// Saving stores each text part's content under a new ID, then saves the
// tree with those IDs; other parts stay unstored until asked for.
func TestContentsSave(t *testing.T) {
	message := uuid.New()
	store := &messagecontenttest.Fake{}
	storage := newPartsMemory(7, message)

	err := messagecontent.NewContents(store, storage).Save(t.Context(), 7, message, newsletter(), newsletterTexts)
	require.NoError(t, err)

	root, ok, err := storage.MessageParts(t.Context(), 7, message)
	require.NoError(t, err)
	require.True(t, ok)
	byID := map[string]messagecontent.Part{}
	for _, part := range flatten(root) {
		byID[part.ID] = part
	}
	plain, html := byID["0.0"].BlobID, byID["0.1.0"].BlobID
	assert.NotEqual(t, uuid.Nil(), plain)
	assert.NotEqual(t, uuid.Nil(), html)
	assert.NotEqual(t, plain, html)
	assert.Equal(t, "Hello\r\n", stored(t, store, messagecontent.Ref{Account: 7, ID: plain}))
	assert.Equal(t, "<p>Hello</p>", stored(t, store, messagecontent.Ref{Account: 7, ID: html}))
	for _, id := range []string{"", "0", "0.1", "0.1.1", "1"} {
		assert.Equal(t, uuid.Nil(), byID[id].BlobID, "part %q", id)
	}
}

// Content is stored before the rows that name it, so a store that fails
// leaves no row naming missing content.
func TestContentsSaveStoreFails(t *testing.T) {
	message := uuid.New()
	refused := errors.New("store down")
	storage := newPartsMemory(7, message)

	err := messagecontent.NewContents(&messagecontenttest.Fake{Fail: refused}, storage).Save(t.Context(), 7, message, newsletter(), newsletterTexts)
	require.ErrorIs(t, err, refused)

	_, ok, err := storage.MessageParts(t.Context(), 7, message)
	require.NoError(t, err)
	assert.False(t, ok)
}

// A message deleted, or one of another account, is not found; a message
// whose parts are saved keeps them.
func TestContentsSaveRefused(t *testing.T) {
	message := uuid.New()
	storage := newPartsMemory(7, message)
	contents := messagecontent.NewContents(&messagecontenttest.Fake{}, storage)

	err := contents.Save(t.Context(), 8, message, newsletter(), newsletterTexts)
	require.ErrorIs(t, err, messagecontent.ErrMessageNotFound)

	require.NoError(t, contents.Save(t.Context(), 7, message, newsletter(), newsletterTexts))
	first, _, err := storage.MessageParts(t.Context(), 7, message)
	require.NoError(t, err)
	require.NoError(t, contents.Save(t.Context(), 7, message, newsletter(), newsletterTexts))
	again, _, err := storage.MessageParts(t.Context(), 7, message)
	require.NoError(t, err)
	assert.Equal(t, first, again)
}

// Saving a saved message again stores its content under the IDs its rows
// name, so content the store lost comes back.
func TestContentsSaveRestores(t *testing.T) {
	message := uuid.New()
	store := &messagecontenttest.Fake{}
	storage := newPartsMemory(7, message)
	contents := messagecontent.NewContents(store, storage)
	require.NoError(t, contents.Save(t.Context(), 7, message, newsletter(), newsletterTexts))
	root, _, err := storage.MessageParts(t.Context(), 7, message)
	require.NoError(t, err)
	lost := messagecontent.Ref{Account: 7, ID: root.Parts[0].Parts[0].BlobID}
	require.NoError(t, store.Delete(t.Context(), []messagecontent.Ref{lost}))

	require.NoError(t, contents.Save(t.Context(), 7, message, newsletter(), newsletterTexts))

	assert.Equal(t, "Hello\r\n", stored(t, store, lost))
	again, _, err := storage.MessageParts(t.Context(), 7, message)
	require.NoError(t, err)
	assert.Equal(t, root, again)
}
