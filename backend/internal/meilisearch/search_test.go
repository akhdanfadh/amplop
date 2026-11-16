package meilisearch

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
		assert.Equal(t, []uuid.UUID{want}, ids(result), text)
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
	assert.Equal(t, []uuid.UUID{shown.ID, hidden.ID}, ids(result))
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
	assert.Len(t, ids(found), 1)
	assert.NotContains(t, ids(found), uuid.MustParse(stray["id"].(string)))
	require.NoError(t, keptErr)
}

// Scopes joined with OR stay inside the other filters: a stray document
// of another owner's that every scope matches is not found, and the
// account, sender, and date filters still hold for each scope, in the
// hits and in the total.
func TestScopesFilterStray(t *testing.T) {
	// Given the owner's labelled message and starred one, one of each in
	// another account, from another sender, and on another day, and a
	// stray document of another owner's in both places.
	index := newIndex(t)
	label, spam := uuid.New(), uuid.New()
	labelled := message("Invoice")
	labelled.ConversationMailboxIDs = []uuid.UUID{label}
	labelled.FromAddresses = []string{"fajar@example.com"}
	starred := message("Invoice")
	starred.ConversationFlagged = true
	starred.FromAddresses = labelled.FromAddresses
	otherAccount := starred
	otherAccount.ID, otherAccount.ThreadID, otherAccount.ConnectedAccountID = uuid.New(), "thread-a", account+1
	otherSender := labelled
	otherSender.ID, otherSender.ThreadID, otherSender.FromAddresses = uuid.New(), "thread-s", []string{"budi@example.com"}
	otherDay := starred
	otherDay.ID, otherDay.ThreadID, otherDay.InternalDate = uuid.New(), "thread-d", starred.InternalDate.AddDate(0, 0, -2)
	labelled.ThreadID, starred.ThreadID = "thread-l", "thread-f"
	require.NoError(t, index.Upsert(t.Context(), []search.Document{labelled, starred, otherAccount, otherSender, otherDay}))
	stray := map[string]any{
		"id": uuid.New().String(), "owner_user_id": "2", "connected_account_id": decimal(account), "subject": "Invoice",
		"conversation": "stray", "conversation_mailbox_ids": []string{label.String()}, "conversation_flagged": true,
		"mailbox_ids": []string{}, "from_addresses": labelled.FromAddresses, "internal_date": labelled.InternalDate.UnixMilli(),
	}
	seed(t, index, owner, stray)

	// When the owner searches both places, in one account, from one
	// sender, on one day.
	found, err := index.Search(t.Context(), owner, search.Query{
		Text:     "invoice",
		Accounts: []int64{account},
		Scopes: []search.Scope{
			{InConversation: []uuid.UUID{label}, Exclude: []uuid.UUID{spam}},
			{Flagged: true, Exclude: []uuid.UUID{spam}},
		},
		From:     labelled.FromAddresses,
		Since:    labelled.InternalDate.Add(-time.Hour),
		Before:   labelled.InternalDate.Add(time.Hour),
		Page:     1,
		PageSize: 10,
	})
	require.NoError(t, err)

	// Then only the two that pass every filter are found and counted.
	assert.ElementsMatch(t, []uuid.UUID{labelled.ID, starred.ID}, ids(found))
	assert.Equal(t, 2, found.Total)
}

// A sender the member types stays one value of its list, however it is
// written: it neither reaches a stray document of another owner's in the
// index, nor turns into expressions of its own.
func TestSenderFilterStray(t *testing.T) {
	// Given the owner's message from Fajar, and a stray document of
	// another owner's that every widened filter would match.
	index := newIndex(t)
	mine := message("Invoice")
	mine.FromAddresses = []string{"fajar@example.com"}
	require.NoError(t, index.Upsert(t.Context(), []search.Document{mine}))
	stray := map[string]any{
		"id": uuid.New().String(), "owner_user_id": "2", "connected_account_id": "11", "subject": "Invoice",
		"from_addresses": []string{"x", "fajar@example.com"}, "has_attachment": true, "internal_date": 1,
	}
	seed(t, index, owner, stray)

	// When the owner searches with crafted senders beside a real one, and
	// with crafted ones alone.
	sent := func(from ...string) []uuid.UUID {
		result, err := index.Search(t.Context(), owner, fromQuery(from))
		require.NoError(t, err)
		return ids(result)
	}
	crafted := []string{
		`x" OR owner_user_id = "2`,
		`x"] OR owner_user_id = "2" OR from_addresses IN ["x`,
		`x\" OR has_attachment = true OR from_addresses = "x`,
		`x\`,
	}

	// Then only the owner's message from Fajar is ever found.
	assert.Equal(t, []uuid.UUID{mine.ID}, sent(append(crafted, "fajar@example.com")...))
	for _, from := range crafted {
		assert.Empty(t, sent(from), from)
	}
}

// fromQuery returns a search without words for messages from any of
// from, on the first page.
func fromQuery(from []string) search.Query {
	return search.Query{From: from, Page: 1, PageSize: 10}
}

// A search without words asks for IDs alone: with nothing to mark, cutting
// excerpts would be wasted time.
func TestSearchNoWordsAsksIDsOnly(t *testing.T) {
	index := newIndex(t)
	require.NoError(t, index.Upsert(t.Context(), []search.Document{message("Invoice")}))
	var body map[string]any
	index.client = proxied(t, func(r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/search") {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			_ = json.Unmarshal(raw, &body)
		}
	})

	result, err := index.Search(t.Context(), owner, search.Query{Page: 1, PageSize: 10})
	require.NoError(t, err)

	assert.Len(t, ids(result), 1)
	require.NotNil(t, body)
	for _, field := range []string{"attributesToCrop", "attributesToHighlight", "showMatchesPosition"} {
		assert.NotContains(t, body, field)
	}
}

// A bound is the index's milliseconds, rounded up, so a date finer than a
// millisecond keeps the same messages as a bound exactly at it.
func TestMilliUp(t *testing.T) {
	at := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	cases := map[time.Duration]int64{
		0:                          at.UnixMilli(),
		time.Nanosecond:            at.UnixMilli() + 1,
		time.Millisecond:           at.UnixMilli() + 1,
		time.Millisecond + 1:       at.UnixMilli() + 2,
		-time.Nanosecond:           at.UnixMilli(),
		-time.Millisecond:          at.UnixMilli() - 1,
		-time.Millisecond - 1:      at.UnixMilli() - 1,
		-time.Millisecond + 1:      at.UnixMilli(),
		999*time.Millisecond + 999: at.UnixMilli() + 1000,
	}
	for offset, want := range cases {
		assert.Equal(t, want, milliUp(at.Add(offset)), "%v", offset)
	}
	// Before 1970, where Unix time is negative.
	assert.Equal(t, int64(-1000), milliUp(time.Unix(-1, 0)))
	assert.Equal(t, int64(-999), milliUp(time.Unix(-1, 1)))
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

// Each word narrows a search, a number is never a typo away from another,
// and of equal matches the newest comes first, whatever order they were
// written in.
func TestSearchRelevance(t *testing.T) {
	index := newIndex(t)
	budi := message("Invoice from Budi")
	shop := message("Invoice from Shop")
	order := message("Order 4471829")
	nearOrder := message("Order 4471828")
	older, newer := message("Receipt"), message("Receipt")
	older.InternalDate = newer.InternalDate.Add(-24 * time.Hour)
	require.NoError(t, index.Upsert(t.Context(), []search.Document{budi, shop, order, nearOrder, older, newer}))

	cases := map[string][]uuid.UUID{
		"invoice budi": {budi.ID},
		"4471829":      {order.ID},
		"recept":       {newer.ID, older.ID}, // a word still allows a typo
	}
	for text, want := range cases {
		result, err := index.Search(t.Context(), owner, search.Query{Text: text, Page: 1, PageSize: 10})
		require.NoError(t, err, text)
		assert.Equal(t, want, ids(result), text)
	}
}

// An excerpt is the body text around the best match, cut by Meilisearch,
// with every match marked, typos too, counted in bytes; the hidden text
// gives it only when the shown text has no match.
func TestSearchExcerpt(t *testing.T) {
	index := newIndex(t)
	long := message("Booking 7781")
	long.BodyText = "Dear Asha, thank you for booking with us. Your trip starts on Monday. " +
		"At the counter, please bring your passport and the printed ticket. Café hours are posted " +
		"in the lobby, and the shuttle leaves every hour from the north gate of the terminal."
	hidden := message("Sale")
	hidden.BodyText = "Our shoes are on sale."
	hidden.AltText = "Our pasport offers are here."
	require.NoError(t, index.Upsert(t.Context(), []search.Document{long, hidden}))

	result, err := index.Search(t.Context(), owner, search.Query{Text: "passport", Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.Len(t, result.Hits, 2)
	byID := map[uuid.UUID]search.Hit{result.Hits[0].ID: result.Hits[0], result.Hits[1].ID: result.Hits[1]}
	inLong, inHidden := byID[long.ID], byID[hidden.ID]

	assert.True(t, strings.HasPrefix(inLong.Excerpt, "…"), inLong.Excerpt)
	assert.Contains(t, inLong.Excerpt, "bring your passport and")
	assert.NotContains(t, inLong.Excerpt, "Dear Asha")
	require.Len(t, inLong.ExcerptMatches, 1)
	assert.Equal(t, "passport", marked(inLong.Excerpt, inLong.ExcerptMatches[0]))
	require.Len(t, inHidden.ExcerptMatches, 1)
	assert.Equal(t, "pasport", marked(inHidden.Excerpt, inHidden.ExcerptMatches[0]))
	assert.Empty(t, inLong.SubjectMatches)
}

// marked returns the text span marks in text.
func marked(text string, span search.Span) string {
	return text[span.Start : span.Start+span.Length]
}

// A search Meilisearch does not answer, or answers that it failed, is
// [search.ErrUnavailable]; one it refuses is not.
func TestSearchUnavailable(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(failing.Close)
	refused := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	refused.Close()
	q := search.Query{Text: "invoice", Page: 1, PageSize: 10}

	_, failed := New(failing.URL, masterKey).Search(t.Context(), owner, q)
	_, unanswered := New(refused.URL, masterKey).Search(t.Context(), owner, q)
	_, forbidden := New(address, "not-the-key").Search(t.Context(), owner, q)

	require.ErrorIs(t, failed, search.ErrUnavailable)
	require.ErrorIs(t, unanswered, search.ErrUnavailable)
	require.Error(t, forbidden)
	assert.NotErrorIs(t, forbidden, search.ErrUnavailable)
}

// The marks come out of an excerpt, each marking what it held.
func TestUnmark(t *testing.T) {
	cases := map[string]struct {
		text    string
		want    string
		matches []search.Span
	}{
		"none":          {"plain text", "plain text", nil},
		"two":           {"a \x02bc\x03 d \x02e\x03", "a bc d e", []search.Span{{Start: 2, Length: 2}, {Start: 7, Length: 1}}},
		"bytes":         {"é \x02ü\x03", "é ü", []search.Span{{Start: 3, Length: 2}}},
		"unclosed":      {"a \x02bc", "a bc", []search.Span{{Start: 2, Length: 2}}},
		"stray end":     {"a\x03 b", "a b", nil},
		"empty":         {"a \x02\x03b", "a b", nil},
		"start in mark": {"\x02a\x02b\x03", "ab", []search.Span{{Start: 0, Length: 2}}},
	}
	for name, c := range cases {
		text, matches := unmark(c.text)
		assert.Equal(t, c.want, text, name)
		assert.Equal(t, c.matches, matches, name)
	}
}

// A mark reaches the end of its word when at most two letters of it are
// left, as a typo leaves, and stays as typed otherwise, as a prefix is.
func TestComplete(t *testing.T) {
	cases := map[string]struct {
		text   string
		marked string // the text the match starts with
		want   string
	}{
		"typo":                {"Anggaran rapat", "Anggara", "Anggaran"},
		"typo, two left":      {"Invoice due", "Invoi", "Invoice"},
		"prefix":              {"Invoice due", "Inv", "Inv"},
		"whole word":          {"Invoice due", "Invoice", "Invoice"},
		"end of text":         {"due Invoic", "Invoic", "Invoic"},
		"accent":              {"Caf\u00e9 open", "Caf", "Caf\u00e9"},
		"combining accent":    {"Cafe\u0301 open", "Caf", "Cafe\u0301"},
		"digits":              {"order 44718 now", "447", "44718"},
		"punctuation":         {"Invoic. Next", "Invoic", "Invoic"},
		"no spaces":           {"東京都庁舎です", "東", "東"},
		"no spaces, two left": {"東京都", "東", "東京都"},
	}
	for name, c := range cases {
		start := strings.Index(c.text, c.marked)
		got := complete(c.text, []search.Span{{Start: start, Length: len(c.marked)}})
		assert.Equal(t, []search.Span{{Start: start, Length: len(c.want)}}, got, name)
	}
}

// A typo is marked as the whole word it matched, in the subject and in
// the excerpt, whether it is the query's last word or not; a prefix, the
// last word typed so far, is marked as typed.
func TestSearchTypoMarks(t *testing.T) {
	index := newIndex(t)
	m := message("Anggaran rapat")
	m.BodyText = "Berikut draf anggaran untuk rapat Desember."
	require.NoError(t, index.Upsert(t.Context(), []search.Document{m}))
	cases := map[string]struct{ subject, excerpt []string }{
		"anggran":       {[]string{"Anggaran"}, []string{"anggaran"}},
		"anggran rapat": {[]string{"Anggaran", "rapat"}, []string{"anggaran", "rapat"}},
		"rapat anggran": {[]string{"Anggaran", "rapat"}, []string{"anggaran", "rapat"}},
		"angg":          {[]string{"Angg"}, []string{"angg"}},
	}

	for text, want := range cases {
		result, err := index.Search(t.Context(), owner, search.Query{Text: text, Page: 1, PageSize: 10})
		require.NoError(t, err, text)

		require.Len(t, result.Hits, 1, text)
		hit := result.Hits[0]
		assert.Equal(t, m.Subject, hit.Subject, text)
		assert.Equal(t, want.subject, allMarked(hit.Subject, hit.SubjectMatches), text)
		assert.Equal(t, want.excerpt, allMarked(hit.Excerpt, hit.ExcerptMatches), text)
	}
}

// allMarked returns the text each span marks in text, in order.
func allMarked(text string, spans []search.Span) []string {
	out := []string{}
	for _, s := range spans {
		out = append(out, marked(text, s))
	}
	return out
}

// A search asks for marks, never for every match's position, which lists
// each match in both body texts and made a crafted page decode to about
// 500 MB.
func TestSearchAsksNoPositions(t *testing.T) {
	index := newIndex(t)
	require.NoError(t, index.Upsert(t.Context(), []search.Document{message("Invoice")}))
	var body map[string]any
	index.client = proxied(t, func(r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/search") {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			_ = json.Unmarshal(raw, &body)
		}
	})

	_, err := index.Search(t.Context(), owner, search.Query{Text: "invoice", Page: 1, PageSize: 10})
	require.NoError(t, err)

	require.NotNil(t, body)
	assert.NotContains(t, body, "showMatchesPosition")
	assert.Equal(t, []any{"id"}, body["attributesToRetrieve"])
}
