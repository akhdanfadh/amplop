package search_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/mailsync"
	"inbok/internal/search"
	"inbok/internal/search/searchtest"
)

// stored is a message storage holds, and the mailboxes it is in now.
type stored struct {
	message   search.Message
	mailboxes []uuid.UUID
}

// searchMemory is what a search reads, in memory: members' accounts,
// mailboxes, and messages.
type searchMemory struct {
	accounts  map[int64]int64 // each account's member
	roles     map[uuid.UUID]mailsync.Role
	owners    map[uuid.UUID]int64 // each mailbox's member
	messages  map[int64][]stored  // each member's messages
	fail      error               // returned by every read when set
	readRoles [][]mailsync.Role   // the hidden roles each read of messages named
}

func newSearchMemory() *searchMemory {
	return &searchMemory{
		accounts: map[int64]int64{ashaWork: asha, ashaHome: asha, budiWork: budi},
		roles:    map[uuid.UUID]mailsync.Role{},
		owners:   map[uuid.UUID]int64{},
		messages: map[int64][]stored{},
	}
}

func (m *searchMemory) OwnsAccount(_ context.Context, owner, accountID int64) (bool, error) {
	if m.fail != nil {
		return false, m.fail
	}
	return m.accounts[accountID] == owner, nil
}

func (m *searchMemory) MailboxIDs(_ context.Context, owner int64, roles []mailsync.Role) ([]uuid.UUID, error) {
	if m.fail != nil {
		return nil, m.fail
	}
	var ids []uuid.UUID
	for id, role := range m.roles {
		if m.owners[id] == owner && slices.Contains(roles, role) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (m *searchMemory) FoundMessages(_ context.Context, owner int64, ids []uuid.UUID, hidden []mailsync.Role) ([]search.Message, error) {
	if m.fail != nil {
		return nil, m.fail
	}
	m.readRoles = append(m.readRoles, hidden)
	var out []search.Message
	// Backwards, so a search that kept storage's order would fail.
	for _, s := range slices.Backward(m.messages[owner]) {
		in := slices.ContainsFunc(s.mailboxes, func(id uuid.UUID) bool { return slices.Contains(hidden, m.roles[id]) })
		if slices.Contains(ids, s.message.ID) && !in {
			out = append(out, s.message)
		}
	}
	return out, nil
}

// mailbox adds owner's mailbox with role and returns its ID.
func (m *searchMemory) mailbox(owner int64, role mailsync.Role) uuid.UUID {
	id := uuid.New()
	m.roles[id], m.owners[id] = role, owner
	return id
}

// More accounts of the search tests.
const (
	ashaHome int64 = 12
	budiWork int64 = 21
)

// searchStack is a searcher with its fakes.
type searchStack struct {
	searcher *search.Searcher
	index    *searchtest.Fake
	memory   *searchMemory
	inbox    uuid.UUID // Asha's inbox
}

func newSearchStack() searchStack {
	memory := newSearchMemory()
	index := &searchtest.Fake{}
	return searchStack{searcher: search.NewSearcher(index, memory), index: index, memory: memory, inbox: memory.mailbox(asha, mailsync.RoleInbox)}
}

// add stores owner's message in account with subject and body in
// mailboxes, and indexes it so.
func (s searchStack) add(t *testing.T, owner, account int64, subject, body string, mailboxes ...uuid.UUID) search.Message {
	t.Helper()
	m := search.Message{
		ID:        uuid.New(),
		AccountID: account,
		Date:      time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC),
		From:      []mailsync.Address{{Name: "Citra", Address: "citra@example.com"}},
		Subject:   subject,
		Preview:   "Gmail's snippet of " + subject,
		Unread:    true,
	}
	s.memory.messages[owner] = append(s.memory.messages[owner], stored{message: m, mailboxes: mailboxes})
	require.NoError(t, s.index.Upsert(t.Context(), []search.Document{{
		ID:                 m.ID,
		OwnerUserID:        owner,
		ConnectedAccountID: account,
		ThreadID:           "thread-" + m.ID.String(),
		InternalDate:       m.Date,
		Subject:            subject,
		BodyText:           body,
		MailboxIDs:         mailboxes,
	}}))
	return m
}

// request returns a search for text on the first page of 20.
func request(text string) search.Request {
	return search.Request{Text: text, Page: 1, Limit: 20}
}

// A search shows each message as storage holds it, in the index's order,
// with where the query matched its subject and its body text.
func TestSearch(t *testing.T) {
	s := newSearchStack()
	first := s.add(t, asha, ashaWork, "Your invoice", "Hello Asha, please pay the invoice today.", s.inbox)
	second := s.add(t, asha, ashaHome, "Receipt", "The invoice is paid.", s.inbox)

	page, err := s.searcher.Search(t.Context(), asha, request("invoice"))
	require.NoError(t, err)

	assert.Equal(t, search.Page{Total: 2, Found: []search.Found{
		{
			Message:        first,
			SubjectMatches: []search.Span{{Start: len("Your "), Length: len("invoice")}},
			Preview:        "…please pay the invoice today.",
			PreviewMatches: []search.Span{{Start: len("…please pay the "), Length: len("invoice")}},
		},
		{
			Message:        second,
			Preview:        "The invoice is paid.",
			PreviewMatches: []search.Span{{Start: len("The "), Length: len("invoice")}},
		},
	}}, page)
}

// A message whose body text the query did not match shows the provider's
// preview, with nothing marked.
func TestSearchPreview(t *testing.T) {
	s := newSearchStack()
	m := s.add(t, asha, ashaWork, "Invoice", "Thank you.", s.inbox)

	page, err := s.searcher.Search(t.Context(), asha, request("invoice"))
	require.NoError(t, err)

	require.Len(t, page.Found, 1)
	assert.Equal(t, m.Preview, page.Found[0].Preview)
	assert.Empty(t, page.Found[0].PreviewMatches)
}

// Spam, Trash, and drafts are left out, whether the index knows a message
// is there or only storage does: a message moved to Trash since the index
// saw it still counts in the total until the index catches up.
func TestSearchHidden(t *testing.T) {
	s := newSearchStack()
	spam := s.memory.mailbox(asha, mailsync.RoleJunk)
	trash := s.memory.mailbox(asha, mailsync.RoleTrash)
	drafts := s.memory.mailbox(asha, mailsync.RoleDrafts)
	shown := s.add(t, asha, ashaWork, "Invoice", "", s.inbox)
	s.add(t, asha, ashaWork, "Invoice", "", s.inbox, spam)
	s.add(t, asha, ashaWork, "Invoice", "", trash)
	s.add(t, asha, ashaWork, "Invoice", "", drafts)
	moved := s.add(t, asha, ashaWork, "Invoice", "", s.inbox)
	s.memory.messages[asha][len(s.memory.messages[asha])-1].mailboxes = []uuid.UUID{trash}

	page, err := s.searcher.Search(t.Context(), asha, request("invoice"))
	require.NoError(t, err)

	require.Len(t, page.Found, 1)
	assert.Equal(t, shown.ID, page.Found[0].Message.ID)
	assert.NotEqual(t, moved.ID, page.Found[0].Message.ID)
	assert.Equal(t, 2, page.Total)
	assert.Equal(t, [][]mailsync.Role{{mailsync.RoleJunk, mailsync.RoleTrash, mailsync.RoleDrafts}}, s.memory.readRoles)
}

// A search the index finds nothing for reads no messages from storage.
func TestSearchNothingFound(t *testing.T) {
	s := newSearchStack()
	s.add(t, asha, ashaWork, "Receipt", "", s.inbox)

	page, err := s.searcher.Search(t.Context(), asha, request("invoice"))
	require.NoError(t, err)

	assert.Equal(t, search.Page{Found: []search.Found{}}, page)
	assert.Empty(t, s.memory.readRoles)
}

// A message deleted since the index saw it is left out.
func TestSearchDeletedSince(t *testing.T) {
	s := newSearchStack()
	kept := s.add(t, asha, ashaWork, "Invoice", "", s.inbox)
	s.add(t, asha, ashaWork, "Invoice", "", s.inbox)
	s.memory.messages[asha] = s.memory.messages[asha][:1]

	page, err := s.searcher.Search(t.Context(), asha, request("invoice"))
	require.NoError(t, err)

	require.Len(t, page.Found, 1)
	assert.Equal(t, kept.ID, page.Found[0].Message.ID)
}

// A search of one account finds only its messages; one of an account the
// member does not have, another member's included, is refused.
func TestSearchAccount(t *testing.T) {
	s := newSearchStack()
	work := s.add(t, asha, ashaWork, "Invoice", "", s.inbox)
	s.add(t, asha, ashaHome, "Invoice", "", s.inbox)
	s.add(t, budi, budiWork, "Invoice", "")

	one := request("invoice")
	one.Account = ashaWork
	page, err := s.searcher.Search(t.Context(), asha, one)
	require.NoError(t, err)
	foreign := request("invoice")
	foreign.Account = budiWork
	_, foreignErr := s.searcher.Search(t.Context(), asha, foreign)
	missing := request("invoice")
	missing.Account = 99
	_, missingErr := s.searcher.Search(t.Context(), asha, missing)

	require.Len(t, page.Found, 1)
	assert.Equal(t, work.ID, page.Found[0].Message.ID)
	require.ErrorIs(t, foreignErr, search.ErrAccountNotFound)
	require.ErrorIs(t, missingErr, search.ErrAccountNotFound)
}

// Matches on a subject the index holds differently from storage are left
// out, so no other words are marked.
func TestSearchSubjectChanged(t *testing.T) {
	s := newSearchStack()
	m := s.add(t, asha, ashaWork, "Invoice", "", s.inbox)
	s.memory.messages[asha][0].message.Subject = "Re: Invoice"

	page, err := s.searcher.Search(t.Context(), asha, request("invoice"))
	require.NoError(t, err)

	require.Len(t, page.Found, 1)
	assert.Equal(t, m.ID, page.Found[0].Message.ID)
	assert.Equal(t, "Re: Invoice", page.Found[0].Message.Subject)
	assert.Empty(t, page.Found[0].SubjectMatches)
}

// An excerpt keeps at most three words before its first match, so the
// match shows on a narrow screen.
func TestSearchExcerptLead(t *testing.T) {
	cases := map[string]struct {
		body    string
		preview string
	}{
		"short":        {"one two invoice three", "one two invoice three"},
		"three before": {"one two three invoice", "one two three invoice"},
		"cut":          {"one two three four five invoice six", "…three four five invoice six"},
		"spaces":       {"one two\n\nthree  four\tfive invoice", "…three  four\tfive invoice"},
		"characters":   {"zéro un deux trois quatre invoice", "…deux trois quatre invoice"},
		"cut already":  {"…two three four five invoice", "…three four five invoice"},
	}
	for name, c := range cases {
		s := newSearchStack()
		s.add(t, asha, ashaWork, "Hello", c.body, s.inbox)

		page, err := s.searcher.Search(t.Context(), asha, request("invoice"))
		require.NoError(t, err, name)

		require.Len(t, page.Found, 1, name)
		found := page.Found[0]
		assert.Equal(t, c.preview, found.Preview, name)
		require.Len(t, found.PreviewMatches, 1, name)
		match := found.PreviewMatches[0]
		assert.Equal(t, "invoice", found.Preview[match.Start:match.Start+match.Length], name)
	}
}

// Each filter of a request reaches the index, with words or without.
func TestSearchFilters(t *testing.T) {
	s := newSearchStack()
	kept := s.add(t, asha, ashaWork, "Invoice", "", s.inbox)
	other := s.add(t, asha, ashaWork, "Invoice", "", s.inbox)
	document, ok := s.index.Document(asha, kept.ID)
	require.True(t, ok)
	document.FromAddresses = []string{"dewi@example.com"}
	document.HasAttachment = true
	require.NoError(t, s.index.Upsert(t.Context(), []search.Document{document}))
	at := kept.Date

	cases := map[string]struct {
		request search.Request
		want    []uuid.UUID
	}{
		"from":       {search.Request{From: []string{"DEWI@example.com"}}, []uuid.UUID{kept.ID}},
		"attachment": {search.Request{Text: "invoice", WithAttachment: true}, []uuid.UUID{kept.ID}},
		"since":      {search.Request{Since: at.Add(time.Millisecond)}, nil},
		"before":     {search.Request{Before: at}, nil},
		"within":     {search.Request{Since: at, Before: at.Add(time.Millisecond)}, []uuid.UUID{kept.ID, other.ID}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			c.request.Page, c.request.Limit = 1, 20

			page, err := s.searcher.Search(t.Context(), asha, c.request)
			require.NoError(t, err)

			var ids []uuid.UUID
			for _, f := range page.Found {
				ids = append(ids, f.Message.ID)
			}
			assert.ElementsMatch(t, c.want, ids)
		})
	}
}

// An index that does not answer fails the search with
// [search.ErrUnavailable]; storage that fails fails it too.
func TestSearchFails(t *testing.T) {
	s := newSearchStack()
	s.add(t, asha, ashaWork, "Invoice", "", s.inbox)
	s.index.Fail = fmt.Errorf("search: %w", search.ErrUnavailable)
	_, unavailable := s.searcher.Search(t.Context(), asha, request("invoice"))
	s.index.Fail = nil
	broken := errors.New("database unavailable")
	s.memory.fail = broken
	_, storageErr := s.searcher.Search(t.Context(), asha, request("invoice"))

	require.ErrorIs(t, unavailable, search.ErrUnavailable)
	require.ErrorIs(t, storageErr, broken)
}

// No owner, no page, or a range nothing is in, is refused before
// anything is read.
func TestSearchRefused(t *testing.T) {
	s := newSearchStack()
	s.memory.fail = errors.New("must not be read")
	at := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)

	_, noOwner := s.searcher.Search(t.Context(), 0, request("invoice"))
	_, noPage := s.searcher.Search(t.Context(), asha, search.Request{Text: "invoice", Limit: 20})
	swapped := request("invoice")
	swapped.Since, swapped.Before = at, at
	_, noRange := s.searcher.Search(t.Context(), asha, swapped)

	require.ErrorIs(t, noOwner, search.ErrNoOwner)
	require.ErrorIs(t, noPage, search.ErrBadPage)
	require.ErrorIs(t, noRange, search.ErrBadRange)
}
