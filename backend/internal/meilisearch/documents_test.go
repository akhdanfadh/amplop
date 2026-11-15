package meilisearch

import (
	"net/http"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/search"
)

// Documents are stored under ADR005's field names, IDs as decimal
// strings, the date as Unix seconds, and the conversation as the account
// and the thread.
func TestUpsertFields(t *testing.T) {
	index := newIndex(t)
	inbox := uuid.New()
	d := search.Document{
		ID:                 uuid.New(),
		OwnerUserID:        owner,
		ConnectedAccountID: account,
		ThreadID:           "19a0c3f2e1d4b5a6",
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
		"conversation":         "11/19a0c3f2e1d4b5a6",
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

// An upsert applies the settings before it adds the documents, so a
// member's first documents never sit in an index without them, where
// every search, which filters on the owner, would be refused.
func TestUpsertSettingsFirst(t *testing.T) {
	index := newIndex(t)
	member := "/indexes/" + index.uid(owner)
	var asked []string
	index.client = proxied(t, func(r *http.Request) {
		if strings.HasPrefix(r.URL.Path, member+"/") && r.Method != http.MethodGet {
			asked = append(asked, r.Method+" "+strings.TrimPrefix(r.URL.Path, member))
		}
	})

	require.NoError(t, index.Upsert(t.Context(), []search.Document{message("Invoice")}))

	assert.Equal(t, []string{"PATCH /settings", "POST /documents"}, asked)
}
