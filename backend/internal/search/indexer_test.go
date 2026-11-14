package search_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/mailsync"
	"inbok/internal/messagecontent"
	"inbok/internal/search"
	"inbok/internal/search/searchtest"
)

// Members and accounts of the indexer's tests.
const (
	asha     int64 = 1
	ashaWork int64 = 11
	budi     int64 = 2
)

// indexMemory is storage and stored text in memory: each member's
// messages, and the bodies of those with parts.
type indexMemory struct {
	mu      sync.Mutex
	sources map[int64][]search.Source
	bodies  map[uuid.UUID]messagecontent.MessageTexts
	users   map[int64]bool // members with no message saved yet
	fail    error          // returned by every read when set
	read    []uuid.UUID
}

func newIndexMemory() *indexMemory {
	return &indexMemory{sources: map[int64][]search.Source{}, bodies: map[uuid.UUID]messagecontent.MessageTexts{}, users: map[int64]bool{}}
}

func (m *indexMemory) IndexSources(_ context.Context, owner int64, ids []uuid.UUID) ([]search.Source, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return nil, m.fail
	}
	var out []search.Source
	for _, source := range m.sources[owner] {
		if slices.Contains(ids, source.ID) {
			out = append(out, source)
		}
	}
	return out, nil
}

func (m *indexMemory) Read(_ context.Context, _ int64, messageID uuid.UUID, _ messagecontent.TextLimits) (messagecontent.MessageTexts, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return messagecontent.MessageTexts{}, m.fail
	}
	m.read = append(m.read, messageID)
	return m.bodies[messageID], nil
}

func (m *indexMemory) UserIDs(context.Context) ([]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return nil, m.fail
	}
	ids := slices.Collect(maps.Keys(m.users))
	for owner := range m.sources {
		ids = append(ids, owner)
	}
	return ids, nil
}

func (m *indexMemory) UserExists(_ context.Context, owner int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return false, m.fail
	}
	_, ok := m.sources[owner]
	return ok || m.users[owner], nil
}

func (m *indexMemory) MessageIDs(_ context.Context, owner int64) ([]uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return nil, m.fail
	}
	var ids []uuid.UUID
	for _, source := range m.sources[owner] {
		ids = append(ids, source.ID)
	}
	return ids, nil
}

// save keeps source as owner's message.
func (m *indexMemory) save(owner int64, source search.Source) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sources[owner] = append(m.sources[owner], source)
}

// remove deletes owner's message id.
func (m *indexMemory) remove(owner int64, id uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sources[owner] = slices.DeleteFunc(m.sources[owner], func(s search.Source) bool { return s.ID == id })
}

// message returns a message of account in the inbox, with a new ID.
func message(account int64, subject string) search.Source {
	return search.Source{
		ID:           uuid.New(),
		AccountID:    account,
		InternalDate: time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC),
		Subject:      subject,
		From:         []mailsync.Address{{Name: "Citra", Address: "citra@example.com"}},
		To:           []mailsync.Address{{Address: "asha@example.com"}, {Name: "Dewi", Address: "dewi@example.com"}},
		MailboxIDs:   []uuid.UUID{uuid.New()},
	}
}

// indexStack is an indexer with its fakes.
type indexStack struct {
	indexer *search.Indexer
	index   *searchtest.Fake
	memory  *indexMemory
}

func newIndexStack() indexStack {
	memory := newIndexMemory()
	index := &searchtest.Fake{}
	return indexStack{indexer: search.NewIndexer(index, memory, memory), index: index, memory: memory}
}

// A message is indexed as storage holds it: its headers as a reader sees
// them, its mailboxes, the text of the bodies a view shows, and apart
// from it the text of its other text parts.
func TestIndex(t *testing.T) {
	s := newIndexStack()
	m := message(ashaWork, "Invoice")
	m.HasParts = true
	s.memory.save(asha, m)
	s.memory.bodies[m.ID] = messagecontent.MessageTexts{
		Shown: []messagecontent.StoredText{
			{MediaType: "text/html", Content: []byte("<p>Please <b>pay</b></p>")},
			{MediaType: "text/plain", Content: []byte("Thanks")},
		},
		Hidden: []messagecontent.StoredText{{MediaType: "text/plain", Content: []byte("Please pay")}},
	}

	require.NoError(t, s.indexer.Index(t.Context(), asha, []uuid.UUID{m.ID}))
	got, ok := s.index.Document(asha, m.ID)

	require.True(t, ok)
	assert.Equal(t, search.Document{
		ID:                 m.ID,
		OwnerUserID:        asha,
		ConnectedAccountID: ashaWork,
		InternalDate:       m.InternalDate,
		Subject:            "Invoice",
		From:               "Citra <citra@example.com>",
		To:                 []string{"asha@example.com", "Dewi <dewi@example.com>"},
		CC:                 []string{},
		BodyText:           "Please pay Thanks",
		AltText:            "Please pay",
		FromAddress:        "citra@example.com",
		MailboxIDs:         m.MailboxIDs,
	}, got)
}

// A message whose body is not stored yet is found by its headers, and its
// text is never asked for.
func TestIndexHeadersOnly(t *testing.T) {
	s := newIndexStack()
	m := message(ashaWork, "Invoice")
	s.memory.save(asha, m)

	require.NoError(t, s.indexer.Index(t.Context(), asha, []uuid.UUID{m.ID}))
	got, ok := s.index.Document(asha, m.ID)

	require.True(t, ok)
	assert.Empty(t, got.BodyText)
	assert.Empty(t, s.memory.read)
}

// An ID storage no longer has, e.g. a deleted message, is deleted from the
// index, while the other IDs of the batch are upserted.
func TestIndexDeletes(t *testing.T) {
	s := newIndexStack()
	kept, deleted := message(ashaWork, "Kept"), message(ashaWork, "Deleted")
	s.memory.save(asha, kept)
	s.memory.save(asha, deleted)
	require.NoError(t, s.indexer.Index(t.Context(), asha, []uuid.UUID{kept.ID, deleted.ID}))

	s.memory.remove(asha, deleted.ID)
	require.NoError(t, s.indexer.Index(t.Context(), asha, []uuid.UUID{kept.ID, deleted.ID}))
	_, keptOK := s.index.Document(asha, kept.ID)
	_, deletedOK := s.index.Document(asha, deleted.ID)

	assert.True(t, keptOK)
	assert.False(t, deletedOK)
}

// A job queued while a message existed, retried after the message was
// deleted and its delete job ran, never brings it back: each run reads
// the message as it is when it runs.
func TestIndexNeverResurrects(t *testing.T) {
	s := newIndexStack()
	m := message(ashaWork, "Invoice")
	s.memory.save(asha, m)
	saved := []uuid.UUID{m.ID} // the job the save queued

	// The save's job fails, as when Meilisearch is down.
	s.index.Fail = errors.New("index unavailable")
	require.Error(t, s.indexer.Index(t.Context(), asha, saved))
	s.index.Fail = nil

	// The message is deleted, and the delete's job runs.
	s.memory.remove(asha, m.ID)
	require.NoError(t, s.indexer.Index(t.Context(), asha, []uuid.UUID{m.ID}))

	// The save's job is retried, after the delete's.
	require.NoError(t, s.indexer.Index(t.Context(), asha, saved))
	_, ok := s.index.Document(asha, m.ID)

	assert.False(t, ok)
}

// A member's run reads and writes only that member's messages: another
// member's message ID reads as gone, and its delete reaches only the
// member's own index.
func TestIndexOwner(t *testing.T) {
	s := newIndexStack()
	theirs := message(21, "Invoice")
	s.memory.save(budi, theirs)
	require.NoError(t, s.indexer.Index(t.Context(), budi, []uuid.UUID{theirs.ID}))

	require.NoError(t, s.indexer.Index(t.Context(), asha, []uuid.UUID{theirs.ID}))
	_, mine := s.index.Document(asha, theirs.ID)
	_, kept := s.index.Document(budi, theirs.ID)

	assert.False(t, mine)
	assert.True(t, kept)
}

// A failure to read leaves the index as it was and returns the error, so
// the job retries.
func TestIndexFails(t *testing.T) {
	s := newIndexStack()
	m := message(ashaWork, "Invoice")
	m.HasParts = true
	s.memory.save(asha, m)
	failure := errors.New("database unavailable")
	s.memory.fail = failure

	err := s.indexer.Index(t.Context(), asha, []uuid.UUID{m.ID})
	_, ok := s.index.Document(asha, m.ID)

	require.ErrorIs(t, err, failure)
	assert.False(t, ok)
}

// No owner is refused before anything is read.
func TestIndexNoOwner(t *testing.T) {
	s := newIndexStack()
	s.memory.fail = errors.New("must not be read")

	err := s.indexer.Index(t.Context(), 0, []uuid.UUID{uuid.New()})

	require.ErrorIs(t, err, search.ErrNoOwner)
}

// Removing an account removes its messages and keeps the member's others.
func TestRemoveAccount(t *testing.T) {
	s := newIndexStack()
	work, home := message(ashaWork, "Work"), message(12, "Home")
	s.memory.save(asha, work)
	s.memory.save(asha, home)
	require.NoError(t, s.indexer.Index(t.Context(), asha, []uuid.UUID{work.ID, home.ID}))

	require.NoError(t, s.indexer.RemoveAccount(t.Context(), asha, ashaWork))
	_, workOK := s.index.Document(asha, work.ID)
	_, homeOK := s.index.Document(asha, home.ID)

	assert.False(t, workOK)
	assert.True(t, homeOK)
}

// acceptedIndex is a [searchtest.Fake] whose next upsert is applied and
// then reported as failed, as when Meilisearch took a write but its answer
// was lost.
type acceptedIndex struct {
	*searchtest.Fake
	lose bool
}

func (a *acceptedIndex) Upsert(ctx context.Context, documents []search.Document) error {
	if err := a.Fake.Upsert(ctx, documents); err != nil {
		return err
	}
	if a.lose {
		a.lose = false
		return errors.New("answer lost")
	}
	return nil
}

// A write the index applied though its caller saw an error, then a delete,
// then the first job's retry: the message stays deleted, and a retry after
// a mailbox change writes only the current mailboxes.
func TestIndexAcceptedThenRetried(t *testing.T) {
	memory := newIndexMemory()
	index := &acceptedIndex{Fake: &searchtest.Fake{}}
	indexer := search.NewIndexer(index, memory, memory)
	gone, moved := message(ashaWork, "Gone"), message(ashaWork, "Moved")
	memory.save(asha, gone)
	memory.save(asha, moved)

	index.lose = true
	require.Error(t, indexer.Index(t.Context(), asha, []uuid.UUID{gone.ID, moved.ID}))
	memory.remove(asha, gone.ID)
	trash := uuid.New()
	memory.remove(asha, moved.ID)
	moved.MailboxIDs = []uuid.UUID{trash}
	memory.save(asha, moved)
	require.NoError(t, indexer.Index(t.Context(), asha, []uuid.UUID{gone.ID, moved.ID}))
	require.NoError(t, indexer.Index(t.Context(), asha, []uuid.UUID{gone.ID, moved.ID}))
	_, goneOK := index.Document(asha, gone.ID)
	got, movedOK := index.Document(asha, moved.ID)

	assert.False(t, goneOK)
	require.True(t, movedOK)
	assert.Equal(t, []uuid.UUID{trash}, got.MailboxIDs)
}
