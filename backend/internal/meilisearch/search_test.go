package meilisearch

import (
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/search"
)

// Meilisearch's own matching works on the fields ADR005 lists: typos,
// prefixes, quoted phrases, excluded words, and parts of an address.
func TestSearchText(t *testing.T) {
	index := newIndex(t)
	newsletter := message("Quarterly newsletter")
	newsletter.BodyText = "lunch tomorrow with the finance team"
	budget := message("Budget report")
	budget.BodyText = "the finance numbers"
	budget.From = "Asha K <asha.k@example.co.id>"
	weekly := message("Weekly report")
	weekly.BodyText = "team lunch"
	require.NoError(t, index.Upsert(t.Context(), []search.Document{newsletter, budget, weekly}))

	cases := map[string]uuid.UUID{
		"newsleter":        newsletter.ID, // one typo
		"quarter":          newsletter.ID, // a prefix of the last word
		`"finance team"`:   newsletter.ID, // a phrase, not the two words apart
		"report -budget":   weekly.ID,     // an excluded word
		"asha":             budget.ID,     // the name part of an address
		"example.co.id":    budget.ID,     // the domain of an address
		"tomorrow finance": newsletter.ID, // words from the body
	}
	for text, want := range cases {
		result, err := index.Search(t.Context(), owner, search.Query{Text: text, Page: 1, PageSize: 10})
		require.NoError(t, err, text)
		assert.Equal(t, []uuid.UUID{want}, result.IDs, text)
	}
}

// A word only in a message's hidden text finds it, but ranks below the same
// word in text a view shows, whichever was indexed first.
func TestSearchAltText(t *testing.T) {
	index := newIndex(t)
	hidden := message("Sale")
	hidden.AltText = "running shoes"
	shown := message("Sale")
	shown.BodyText = "running shoes"
	require.NoError(t, index.Upsert(t.Context(), []search.Document{hidden, shown}))

	result, err := index.Search(t.Context(), owner, search.Query{Text: "shoes", Page: 1, PageSize: 10})

	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{shown.ID, hidden.ID}, result.IDs)
}

// A document that reached another member's index stays hidden from that
// member's searches and out of reach of their account deletes.
func TestOwnerFilter(t *testing.T) {
	// Given, in this owner's index, a document of another owner's in the
	// same account.
	index := newIndex(t)
	require.NoError(t, index.Upsert(t.Context(), []search.Document{message("Invoice")}))
	stray := map[string]any{"id": uuid.New().String(), "owner_user_id": "2", "connected_account_id": "11", "subject": "Invoice"}
	seed(t, index, owner, stray)

	// When the owner searches and deletes the account.
	found, err := index.Search(t.Context(), owner, search.Query{Text: "invoice", Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.NoError(t, index.DeleteAccount(t.Context(), owner, account))
	var kept map[string]any
	keptErr := member(index, owner).GetDocumentWithContext(t.Context(), stray["id"].(string), nil, &kept)

	// Then the search finds only the owner's message, and the stray one is
	// still there.
	assert.Len(t, found.IDs, 1)
	assert.NotContains(t, found.IDs, uuid.MustParse(stray["id"].(string)))
	require.NoError(t, keptErr)
}

// A filter keeps a quote or a backslash inside the value it ends.
func TestQuote(t *testing.T) {
	cases := map[string]string{
		`SPAM`:     `"SPAM"`,
		`a"b`:      `"a\"b"`,
		`a\b`:      `"a\\b"`,
		`a\"b`:     `"a\\\"b"`,
		`x" OR "y`: `"x\" OR \"y"`,
	}
	for value, want := range cases {
		assert.Equal(t, want, quote(value), value)
	}
}
