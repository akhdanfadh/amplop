package search_test

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/search"
	"amplop/internal/search/searchtest"
)

// rebuildStack is a rebuild with its fakes, and the indexer it writes
// with.
type rebuildStack struct {
	rebuild *search.Rebuild
	indexer *search.Indexer
	index   *searchtest.Fake
	memory  *indexMemory
}

func newRebuildStack() rebuildStack {
	memory := newIndexMemory()
	index := &searchtest.Fake{}
	indexer := search.NewIndexer(index, memory, memory)
	return rebuildStack{rebuild: search.NewRebuild(index, memory, indexer), indexer: indexer, index: index, memory: memory}
}

// An empty index, as on a new Meilisearch, gets every message storage
// has, written as the indexer writes it.
func TestRebuild(t *testing.T) {
	s := newRebuildStack()
	first, second := message(ashaWork, "Invoice"), message(ashaWork, "Receipt")
	s.memory.save(asha, first)
	s.memory.save(asha, second)

	rebuilt, err := s.rebuild.Owner(t.Context(), asha)
	require.NoError(t, err)
	got, ok := s.index.Document(asha, first.ID)
	_, secondOK := s.index.Document(asha, second.ID)

	assert.Equal(t, search.Rebuilt{Messages: 2}, rebuilt)
	require.True(t, ok)
	assert.Equal(t, "Invoice", got.Subject)
	assert.True(t, secondOK)
}

// A document of an earlier release, or one that missed a change, is
// written again as storage holds it now, and a document of a message
// storage no longer has is removed.
func TestRebuildLeftovers(t *testing.T) {
	s := newRebuildStack()
	kept, gone := message(ashaWork, "Invoice"), message(ashaWork, "Deleted")
	s.memory.save(asha, kept)
	s.memory.save(asha, gone)
	require.NoError(t, s.indexer.Index(t.Context(), asha, []uuid.UUID{kept.ID, gone.ID}))
	// Changes the index missed, e.g. while Meilisearch was down.
	s.memory.remove(asha, kept.ID)
	kept.Subject = "Invoice, paid"
	s.memory.save(asha, kept)
	s.memory.remove(asha, gone.ID)

	rebuilt, err := s.rebuild.Owner(t.Context(), asha)
	require.NoError(t, err)
	got, ok := s.index.Document(asha, kept.ID)
	_, goneOK := s.index.Document(asha, gone.ID)

	assert.Equal(t, search.Rebuilt{Messages: 1, Leftovers: 1}, rebuilt)
	require.True(t, ok)
	assert.Equal(t, "Invoice, paid", got.Subject)
	assert.False(t, goneOK)
}

// A document in a member's index of a message that is another member's,
// e.g. after a restored database gave a removed member's ID to a new one,
// is removed from that index only.
func TestRebuildForeignDocument(t *testing.T) {
	s := newRebuildStack()
	theirs := message(21, "Invoice")
	s.memory.save(budi, theirs)
	s.memory.users[asha] = true
	require.NoError(t, s.index.Upsert(t.Context(), []search.Document{
		{ID: theirs.ID, OwnerUserID: asha, ConnectedAccountID: ashaWork, Subject: "Invoice"},
		{ID: theirs.ID, OwnerUserID: budi, ConnectedAccountID: 21, Subject: "Invoice"},
	}))

	rebuilt, err := s.rebuild.Owner(t.Context(), asha)
	require.NoError(t, err)
	_, mine := s.index.Document(asha, theirs.ID)
	_, kept := s.index.Document(budi, theirs.ID)

	assert.Equal(t, search.Rebuilt{Leftovers: 1}, rebuilt)
	assert.False(t, mine)
	assert.True(t, kept)
}

// The index of a member storage no longer has is deleted whole.
func TestRebuildRemovedMember(t *testing.T) {
	s := newRebuildStack()
	require.NoError(t, s.index.Upsert(t.Context(), []search.Document{
		{ID: uuid.New(), OwnerUserID: 9, ConnectedAccountID: 91, Subject: "Invoice"},
	}))

	rebuilt, err := s.rebuild.Owner(t.Context(), 9)
	require.NoError(t, err)
	owners, err := s.index.Owners(t.Context())
	require.NoError(t, err)

	assert.Equal(t, search.Rebuilt{Removed: true}, rebuilt)
	assert.Empty(t, owners)
}

// A member with no mail, and no index, is left without one.
func TestRebuildNoMail(t *testing.T) {
	s := newRebuildStack()
	s.memory.users[asha] = true

	rebuilt, err := s.rebuild.Owner(t.Context(), asha)
	require.NoError(t, err)
	owners, err := s.index.Owners(t.Context())
	require.NoError(t, err)

	assert.Equal(t, search.Rebuilt{}, rebuilt)
	assert.Empty(t, owners)
}

// countingIndexer records the size of each batch it is given, then
// indexes it, failing the batch numbered failAt, from 1, when set.
type countingIndexer struct {
	*search.Indexer
	batches []int
	failAt  int
}

func (c *countingIndexer) Index(ctx context.Context, owner int64, ids []uuid.UUID) error {
	c.batches = append(c.batches, len(ids))
	if len(c.batches) == c.failAt {
		return errors.New("index down")
	}
	return c.Indexer.Index(ctx, owner, ids)
}

// A rebuild that fails part way keeps the batches it wrote, and a repeat
// finishes it: every message written, every leftover removed.
func TestRebuildRetried(t *testing.T) {
	s := newRebuildStack()
	failing := &countingIndexer{Indexer: s.indexer, failAt: 2}
	for range search.BatchSize + 1 {
		s.memory.save(asha, message(ashaWork, "Invoice"))
	}
	leftover := uuid.New()
	require.NoError(t, s.index.Upsert(t.Context(), []search.Document{{ID: leftover, OwnerUserID: asha, ConnectedAccountID: ashaWork}}))

	_, failed := search.NewRebuild(s.index, s.memory, failing).Owner(t.Context(), asha)
	partial, err := s.index.DocumentIDs(t.Context(), asha)
	require.NoError(t, err)
	rebuilt, err := s.rebuild.Owner(t.Context(), asha)
	require.NoError(t, err)
	ids, err := s.index.DocumentIDs(t.Context(), asha)
	require.NoError(t, err)

	require.Error(t, failed)
	assert.Len(t, partial, search.BatchSize+1, "the first batch and the leftover")
	assert.Equal(t, search.Rebuilt{Messages: search.BatchSize + 1, Leftovers: 1}, rebuilt)
	assert.Len(t, ids, search.BatchSize+1)
	assert.NotContains(t, ids, leftover)
}

// Messages are written in batches of at most [search.BatchSize], each
// leftover too.
func TestRebuildBatches(t *testing.T) {
	s := newRebuildStack()
	counting := &countingIndexer{Indexer: s.indexer}
	rebuild := search.NewRebuild(s.index, s.memory, counting)
	for range search.BatchSize + 1 {
		s.memory.save(asha, message(ashaWork, "Invoice"))
	}
	leftovers := make([]search.Document, search.BatchSize+1)
	for n := range leftovers {
		leftovers[n] = search.Document{ID: uuid.New(), OwnerUserID: asha, ConnectedAccountID: ashaWork}
	}
	require.NoError(t, s.index.Upsert(t.Context(), leftovers))

	rebuilt, err := rebuild.Owner(t.Context(), asha)
	require.NoError(t, err)
	ids, err := s.index.DocumentIDs(t.Context(), asha)
	require.NoError(t, err)

	assert.Equal(t, search.Rebuilt{Messages: search.BatchSize + 1, Leftovers: search.BatchSize + 1}, rebuilt)
	assert.Equal(t, []int{search.BatchSize, 1, search.BatchSize, 1}, counting.batches)
	assert.Len(t, ids, search.BatchSize+1)
}

// A failure to read storage at the start returns the error before any
// write, so the rebuild can be repeated.
func TestRebuildFails(t *testing.T) {
	s := newRebuildStack()
	m := message(ashaWork, "Invoice")
	s.memory.save(asha, m)
	failure := errors.New("database unavailable")
	s.memory.fail = failure

	_, err := s.rebuild.Owner(t.Context(), asha)
	_, ok := s.index.Document(asha, m.ID)

	require.ErrorIs(t, err, failure)
	assert.False(t, ok)
}

// No owner is refused before anything is read.
func TestRebuildNoOwner(t *testing.T) {
	s := newRebuildStack()
	s.memory.fail = errors.New("must not be read")

	_, err := s.rebuild.Owner(t.Context(), 0)

	require.ErrorIs(t, err, search.ErrNoOwner)
}

// A rebuild covers each member storage has, with mail or not, and each
// owner the index keeps documents for, once each.
func TestRebuildOwners(t *testing.T) {
	s := newRebuildStack()
	s.memory.save(asha, message(ashaWork, "Invoice"))
	s.memory.users[budi] = true
	require.NoError(t, s.index.Upsert(t.Context(), []search.Document{
		{ID: uuid.New(), OwnerUserID: asha, ConnectedAccountID: ashaWork},
		{ID: uuid.New(), OwnerUserID: 9, ConnectedAccountID: 91},
	}))

	owners, err := s.rebuild.Owners(t.Context())

	require.NoError(t, err)
	assert.Equal(t, []int64{asha, budi, 9}, owners)
}
