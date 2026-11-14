package meilisearch

import (
	"net/http"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/search"
)

// Documents are stored under ADR005's field names, IDs as decimal
// strings, and the date as Unix seconds.
func TestUpsertFields(t *testing.T) {
	index := newIndex(t)
	inbox := uuid.New()
	d := search.Document{
		ID:                 uuid.New(),
		OwnerUserID:        owner,
		ConnectedAccountID: account,
		InternalDate:       time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC),
		Subject:            "Invoice",
		From:               "Asha K <asha.k@example.co.id>",
		To:                 []string{"Budi <budi@example.com>"},
		CC:                 []string{"Citra <citra@example.com>"},
		BodyText:           "Please pay",
		AltText:            "Pay now",
		FromAddress:        "asha.k@example.co.id",
		MailboxIDs:         []uuid.UUID{inbox},
	}

	require.NoError(t, index.Upsert(t.Context(), []search.Document{d}))
	var stored map[string]any
	require.NoError(t, member(index, owner).GetDocumentWithContext(t.Context(), d.ID.String(), nil, &stored))

	assert.Equal(t, map[string]any{
		"id":                   d.ID.String(),
		"owner_user_id":        "1",
		"connected_account_id": "11",
		"internal_date":        float64(1794042000),
		"subject":              "Invoice",
		"from":                 "Asha K <asha.k@example.co.id>",
		"to":                   []any{"Budi <budi@example.com>"},
		"cc":                   []any{"Citra <citra@example.com>"},
		"body_text":            "Please pay",
		"alt_text":             "Pay now",
		"from_address":         "asha.k@example.co.id",
		"mailbox_ids":          []any{inbox.String()},
	}, stored)
}

// An upsert replaces a stored document whole, so a field the new one lacks
// is gone, rather than merging into it.
func TestUpsertReplaces(t *testing.T) {
	// Given a stored document with a field inbok's documents never have.
	index := newIndex(t)
	d := message("Invoice")
	seed(t, index, owner, map[string]any{"id": d.ID.String(), "owner_user_id": "1", "stale": "left over"})

	// When the message is upserted.
	require.NoError(t, index.Upsert(t.Context(), []search.Document{d}))
	var stored map[string]any
	require.NoError(t, member(index, owner).GetDocumentWithContext(t.Context(), d.ID.String(), nil, &stored))

	// Then the old field is gone.
	assert.NotContains(t, stored, "stale")
}

// A member's first upsert creates their index with ADR005's settings.
func TestUpsertCreates(t *testing.T) {
	index := newIndex(t)

	require.NoError(t, index.Upsert(t.Context(), []search.Document{message("Invoice")}))

	assertSettings(t, index, owner)
}

// An index deleted just before an upsert's documents reach it, e.g. by a
// rebuild, comes back with its settings, so its searches still work.
func TestUpsertAfterDelete(t *testing.T) {
	// Given a member's index that a proxy deletes as the next documents
	// arrive.
	index := newIndex(t)
	require.NoError(t, index.Upsert(t.Context(), []search.Document{message("Before")}))
	direct := index.client
	documents := "/indexes/" + index.uid(owner) + "/documents"
	index.client = proxied(t, func(r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != documents {
			return
		}
		task, err := direct.DeleteIndexWithContext(r.Context(), index.uid(owner))
		assert.NoError(t, index.wait(r.Context(), "delete", task, err))
	})

	// When the next message is upserted.
	after := message("After")
	require.NoError(t, index.Upsert(t.Context(), []search.Document{after}))
	index.client = direct
	result, err := index.Search(t.Context(), owner, search.Query{Text: "after", Page: 1, PageSize: 10})

	// Then the index has its settings and the message.
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{after.ID}, result.IDs)
	assertSettings(t, index, owner)
}
