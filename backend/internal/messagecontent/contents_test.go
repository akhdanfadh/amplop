package messagecontent_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/messagecontent"
	"amplop/internal/messagecontent/messagecontenttest"
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

func (m *partsMemory) UnusedBlobs(_ context.Context, ids []uuid.UUID) ([]uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	used := map[uuid.UUID]bool{}
	for _, root := range m.trees {
		for _, part := range flatten(root) {
			used[part.BlobID] = true
		}
	}
	var unused []uuid.UUID
	for _, id := range ids {
		if !used[id] {
			unused = append(unused, id)
		}
	}
	return unused, nil
}

func (m *partsMemory) SavePartBlob(_ context.Context, accountID int64, messageID uuid.UUID, path []int, blobID uuid.UUID) (uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	root, ok := m.trees[messageID]
	if account := m.messages[messageID]; !ok || account != accountID {
		return uuid.Nil(), messagecontent.ErrPartNotFound
	}
	// The trees share no parts with their callers, so a copy is changed.
	root = clonePart(root)
	part := &root
	for _, i := range path {
		if i < 0 || i >= len(part.Parts) {
			return uuid.Nil(), messagecontent.ErrPartNotFound
		}
		part = &part.Parts[i]
	}
	if part.BlobID == uuid.Nil() {
		part.BlobID = blobID
	}
	m.trees[messageID] = root
	return part.BlobID, nil
}

// clonePart returns a copy of part sharing no slice with it.
func clonePart(part messagecontent.Part) messagecontent.Part {
	if part.Parts == nil {
		return part
	}
	children := make([]messagecontent.Part, len(part.Parts))
	for i, child := range part.Parts {
		children[i] = clonePart(child)
	}
	part.Parts = children
	return part
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
// name, so content the store lost comes back and no copy is orphaned.
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
	later := time.Now().Add(25 * time.Hour)
	deleted, _, err := messagecontent.NewCleanup(store, storage, func() time.Time { return later }).DeleteExpired(t.Context())
	require.NoError(t, err)
	assert.Zero(t, deleted, "no orphaned copies")
}

// The sweep deletes content no part names once it is a day old, and keeps
// what a part names and what may still be waiting for its row.
func TestCleanup(t *testing.T) {
	message := uuid.New()
	store := &messagecontenttest.Fake{}
	storage := newPartsMemory(7, message)
	require.NoError(t, messagecontent.NewContents(store, storage).Save(t.Context(), 7, message, newsletter(), newsletterTexts))
	orphans := []messagecontent.Ref{{Account: 7, ID: uuid.New()}, {Account: 9, ID: uuid.New()}}
	for _, ref := range orphans {
		require.NoError(t, store.Put(t.Context(), ref, strings.NewReader("left by a failed save")))
	}

	// Within the day, nothing goes.
	now := time.Now()
	deleted, more, err := messagecontent.NewCleanup(store, storage, func() time.Time { return now }).DeleteExpired(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 0, deleted)
	assert.False(t, more)
	assert.Equal(t, "left by a failed save", stored(t, store, orphans[0]))

	// A day on, the orphans go.
	later := now.Add(25 * time.Hour)
	cleanup := messagecontent.NewCleanup(store, storage, func() time.Time { return later })
	deleted, _, err = cleanup.DeleteExpired(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 2, deleted)
	for _, ref := range orphans {
		_, err := store.Get(t.Context(), ref)
		require.ErrorIs(t, err, messagecontent.ErrNotStored)
	}
	root, _, err := storage.MessageParts(t.Context(), 7, message)
	require.NoError(t, err)
	for _, part := range flatten(root) {
		if part.BlobID != uuid.Nil() {
			stored(t, store, messagecontent.Ref{Account: 7, ID: part.BlobID})
		}
	}

	// Nothing is left to sweep.
	deleted, more, err = cleanup.DeleteExpired(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 0, deleted)
	assert.False(t, more)
}

// One call deletes at most a batch, and reports more when it may have
// left some.
func TestCleanupBatch(t *testing.T) {
	store := &messagecontenttest.Fake{}
	for range messagecontent.CleanupBatch + 1 {
		require.NoError(t, store.Put(t.Context(), messagecontent.Ref{Account: 7, ID: uuid.New()}, strings.NewReader("x")))
	}
	later := time.Now().Add(25 * time.Hour)
	cleanup := messagecontent.NewCleanup(store, newPartsMemory(7), func() time.Time { return later })

	deleted, more, err := cleanup.DeleteExpired(t.Context())
	require.NoError(t, err)
	assert.Equal(t, messagecontent.CleanupBatch, deleted)
	assert.True(t, more)

	deleted, more, err = cleanup.DeleteExpired(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)
	assert.False(t, more)
}

// A store that cannot list fails the sweep.
func TestCleanupListFails(t *testing.T) {
	refused := errors.New("store down")
	cleanup := messagecontent.NewCleanup(&messagecontenttest.Fake{Fail: refused}, newPartsMemory(7), time.Now)

	_, _, err := cleanup.DeleteExpired(t.Context())
	assert.ErrorIs(t, err, refused)
}
