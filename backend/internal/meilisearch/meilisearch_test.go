package meilisearch

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"amplop/internal/search"
	"amplop/internal/search/searchtest"
)

const (
	owner   int64 = 1
	account int64 = 11
)

func TestContract(t *testing.T) {
	searchtest.RunContract(t, func(t *testing.T) search.Index { return newIndex(t) })
}

// A call past its deadline returns the deadline's error, so the API can
// answer 503 rather than 500.
func TestDeadline(t *testing.T) {
	index := newIndex(t)
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()

	_, searchErr := index.Search(ctx, owner, search.Query{Text: "invoice", Page: 1, PageSize: 10})
	upsertErr := index.Upsert(ctx, []search.Document{message("Invoice")})

	require.ErrorIs(t, searchErr, context.DeadlineExceeded)
	require.ErrorIs(t, upsertErr, context.DeadlineExceeded)
}

// A refused request names Meilisearch's code, not its message.
func TestRefusedKey(t *testing.T) {
	index := New(address, "not-the-master-key")

	_, err := index.Search(t.Context(), owner, search.Query{Text: "invoice", Page: 1, PageSize: 10})

	require.EqualError(t, err, "search: Meilisearch answered HTTP 403 invalid_api_key")
}
