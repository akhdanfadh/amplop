package meilisearch

import (
	"testing"
	"uuid"

	meili "github.com/meilisearch/meilisearch-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/search"
)

// Every document ID is listed, page after page.
func TestDocumentIDsPages(t *testing.T) {
	index := newIndex(t)
	index.documentPage = 2
	documents := []search.Document{message("1"), message("2"), message("3"), message("4"), message("5")}
	require.NoError(t, index.Upsert(t.Context(), documents))

	ids, err := index.DocumentIDs(t.Context(), owner)

	require.NoError(t, err)
	want := make([]uuid.UUID, len(documents))
	for n, d := range documents {
		want[n] = d.ID
	}
	assert.ElementsMatch(t, want, ids)
}

// A document whose ID is no message ID fails the listing rather than be
// left out of it.
func TestDocumentIDsMalformed(t *testing.T) {
	index := newIndex(t)
	seed(t, index, owner, map[string]any{fieldID: "not-a-message", fieldOwner: "1"})

	_, err := index.DocumentIDs(t.Context(), owner)

	require.EqualError(t, err, "list search documents: a document's ID is not a message ID")
}

// An index named like a member's but not as inbok names one fails the
// listing rather than be left out of it.
func TestOwnersMalformed(t *testing.T) {
	for _, suffix := range []string{"nobody", "", "0", "-1", "042", "99999999999999999999"} {
		t.Run(suffix, func(t *testing.T) {
			index := newIndex(t)
			task, err := index.client.CreateIndexWithContext(t.Context(), &meili.IndexConfig{Uid: index.prefix + suffix})
			require.NoError(t, index.wait(t.Context(), "create", task, err))

			_, err = index.Owners(t.Context())

			require.ErrorContains(t, err, "names no user ID")
		})
	}
}
