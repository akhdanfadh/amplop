package meilisearch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// [Index.Prepare] applies the index's settings to a member's index that lacks
// them, and applying them again changes nothing.
func TestPrepare(t *testing.T) {
	// Given a member's index that Meilisearch created bare.
	index := newIndex(t)
	seed(t, index, owner, map[string]any{"id": "m1"})

	// When the settings are applied twice.
	require.NoError(t, index.Prepare(t.Context()))
	require.NoError(t, index.Prepare(t.Context()))

	// Then the index has them.
	assertSettings(t, index, owner)
}

// [Index.Prepare] reaches every member's index, past the first page of
// Meilisearch's list of indexes.
func TestPrepareEveryMember(t *testing.T) {
	index := newIndex(t)
	index.listLimit = 2
	owners := []int64{1, 2, 3}
	for _, o := range owners {
		seed(t, index, o, map[string]any{"id": "m1"})
	}

	require.NoError(t, index.Prepare(t.Context()))

	for _, o := range owners {
		assertSettings(t, index, o)
	}
}

// A settings task that Meilisearch accepts and then fails is an error,
// which keeps out the task's message, since it can quote a document.
func TestPrepareFailedTask(t *testing.T) {
	// Given a document whose _geo field Meilisearch can read only once _geo
	// is filterable, and settings that make it so.
	index := newIndex(t)
	seed(t, index, owner, map[string]any{"id": "g1", "_geo": map[string]any{"lat": "not-a-number", "lng": "x"}})
	index.settings.FilterableAttributes = append(index.settings.FilterableAttributes, "_geo")

	// When the settings are applied.
	err := index.Prepare(t.Context())

	// Then the task's failure is the error.
	require.ErrorContains(t, err, "failed: invalid_document_geo_field")
	assert.NotContains(t, err.Error(), "not-a-number")
}
