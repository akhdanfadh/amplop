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

	"inbok/internal/conversation"
	"inbok/internal/mailsync"
	"inbok/internal/search"
	"inbok/internal/search/searchtest"
)

// stored is a message storage holds: the message, the mailboxes it is in
// now, and its conversation, unless it has none.
type stored struct {
	message      search.Message
	mailboxes    []uuid.UUID
	conversation *conversation.Conversation
}

// searchMemory is what a search reads, in memory: members' accounts,
// mailboxes, and messages.
type searchMemory struct {
	accounts map[int64]int64 // each account's member
	roles    map[uuid.UUID]mailsync.Role
	owners   map[uuid.UUID]int64 // each mailbox's member
	labels   map[uuid.UUID]bool  // the members' own mailboxes with no role
	messages map[int64][]stored  // each member's messages
	fail     error               // returned by every read when set
	reads    int                 // how often messages were read
}

func newSearchMemory() *searchMemory {
	return &searchMemory{
		accounts: map[int64]int64{ashaWork: asha, ashaHome: asha, budiWork: budi},
		roles:    map[uuid.UUID]mailsync.Role{},
		owners:   map[uuid.UUID]int64{},
		labels:   map[uuid.UUID]bool{},
		messages: map[int64][]stored{},
	}
}

func (m *searchMemory) OwnsAccount(_ context.Context, owner, accountID int64) (bool, error) {
	if m.fail != nil {
		return false, m.fail
	}
	return m.accounts[accountID] == owner, nil
}

func (m *searchMemory) RoleMailboxes(_ context.Context, owner int64) (map[mailsync.Role][]uuid.UUID, error) {
	if m.fail != nil {
		return nil, m.fail
	}
	out := map[mailsync.Role][]uuid.UUID{}
	for id, role := range m.roles {
		if m.owners[id] == owner && role != mailsync.RoleNone {
			out[role] = append(out[role], id)
		}
	}
	return out, nil
}

func (m *searchMemory) LabelIDs(_ context.Context, owner int64, ids []uuid.UUID) ([]uuid.UUID, error) {
	if m.fail != nil {
		return nil, m.fail
	}
	var out []uuid.UUID
	for _, id := range ids {
		if m.labels[id] && m.owners[id] == owner {
			out = append(out, id)
		}
	}
	return out, nil
}

func (m *searchMemory) FoundMessages(_ context.Context, owner int64, ids []uuid.UUID) ([]search.Stored, error) {
	if m.fail != nil {
		return nil, m.fail
	}
	m.reads++
	var out []search.Stored
	// Backwards, so a search that kept storage's order would fail.
	for _, s := range slices.Backward(m.messages[owner]) {
		if !slices.Contains(ids, s.message.ID) || s.conversation == nil {
			continue
		}
		roles := []mailsync.Role{}
		for _, id := range s.mailboxes {
			roles = append(roles, m.roles[id])
		}
		out = append(out, search.Stored{Message: s.message, Roles: roles, Conversation: *s.conversation})
	}
	return out, nil
}

// mailbox adds owner's mailbox with role and returns its ID.
func (m *searchMemory) mailbox(owner int64, role mailsync.Role) uuid.UUID {
	id := uuid.New()
	m.roles[id], m.owners[id] = role, owner
	return id
}

// label adds owner's own mailbox with no role and returns its ID.
func (m *searchMemory) label(owner int64) uuid.UUID {
	id := m.mailbox(owner, mailsync.RoleNone)
	m.labels[id] = true
	return id
}

// placement is where a conversation is: its mailboxes, whether it is in
// All mail, and whether it is starred.
type placement struct {
	mailboxes []uuid.UUID
	allMail   bool
	starred   bool
}

// conversationIn returns a conversation of account at p, as storage reads
// its places from p's mailboxes.
func (m *searchMemory) conversationIn(account int64, p placement) conversation.Conversation {
	c := conversation.Conversation{ID: uuid.New(), AccountID: account, InAllMail: p.allMail, Flagged: p.starred, LabelIDs: []uuid.UUID{}}
	for _, id := range p.mailboxes {
		switch m.roles[id] {
		case mailsync.RoleInbox:
			c.InInbox = true
		case mailsync.RoleSent:
			c.InSent = true
		case mailsync.RoleJunk:
			c.InSpam = true
		case mailsync.RoleTrash:
			c.InTrash = true
		case mailsync.RoleNone:
			if m.labels[id] {
				c.LabelIDs = append(c.LabelIDs, id)
			}
		}
	}
	return c
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
// mailboxes, alone in its conversation, and indexes it so. Its
// conversation is in the message's mailboxes, or for a message in Spam or
// Trash only there; a draft has none.
func (s searchStack) add(t *testing.T, owner, account int64, subject, body string, mailboxes ...uuid.UUID) stored {
	t.Helper()
	m := search.Message{
		ID:        uuid.New(),
		AccountID: account,
		Date:      time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC),
		From:      []mailsync.Address{{Name: "Citra", Address: "citra@example.com"}},
		Subject:   subject,
		Preview:   "Gmail's snippet of " + subject,
	}
	added := stored{message: m, mailboxes: mailboxes}
	if !s.has(mailboxes, mailsync.RoleDrafts) {
		hidden := s.has(mailboxes, mailsync.RoleJunk) || s.has(mailboxes, mailsync.RoleTrash)
		places := slices.DeleteFunc(slices.Clone(mailboxes), func(id uuid.UUID) bool {
			role := s.memory.roles[id]
			return hidden && role != mailsync.RoleJunk && role != mailsync.RoleTrash
		})
		c := s.memory.conversationIn(account, placement{mailboxes: places, allMail: !hidden})
		added.conversation = &c
	}
	s.memory.messages[owner] = append(s.memory.messages[owner], added)
	s.reindex(t, owner, added, body)
	return added
}

// place moves m's conversation, owner's, to p, e.g. as a reply in the
// thread does, in storage and in the index.
func (s searchStack) place(t *testing.T, owner int64, m stored, p placement) stored {
	t.Helper()
	c := s.memory.conversationIn(m.message.AccountID, p)
	c.ID = m.conversation.ID
	for n, other := range s.memory.messages[owner] {
		if other.message.ID == m.message.ID {
			s.memory.messages[owner][n].conversation = &c
		}
	}
	m.conversation = &c
	document, ok := s.index.Document(owner, m.message.ID)
	require.True(t, ok)
	s.reindex(t, owner, m, document.BodyText)
	return m
}

// reindex indexes owner's message m with body, with its conversation's
// places.
func (s searchStack) reindex(t *testing.T, owner int64, m stored, body string) {
	t.Helper()
	document := search.Document{
		ID:                 m.message.ID,
		OwnerUserID:        owner,
		ConnectedAccountID: m.message.AccountID,
		ThreadID:           "thread-" + m.message.ID.String(),
		InternalDate:       m.message.Date,
		Subject:            m.message.Subject,
		BodyText:           body,
		MailboxIDs:         m.mailboxes,
	}
	if c := m.conversation; c != nil {
		document.ConversationFlagged = c.Flagged
		for id, role := range s.memory.roles {
			in := (role == mailsync.RoleInbox && c.InInbox) || (role == mailsync.RoleSent && c.InSent) ||
				(role == mailsync.RoleJunk && c.InSpam) || (role == mailsync.RoleTrash && c.InTrash) || slices.Contains(c.LabelIDs, id)
			if in && s.memory.owners[id] == owner {
				document.ConversationMailboxIDs = append(document.ConversationMailboxIDs, id)
			}
		}
	}
	require.NoError(t, s.index.Upsert(t.Context(), []search.Document{document}))
}

// has reports whether any of mailboxes has role.
func (s searchStack) has(mailboxes []uuid.UUID, role mailsync.Role) bool {
	return slices.ContainsFunc(mailboxes, func(id uuid.UUID) bool { return s.memory.roles[id] == role })
}

// ids returns the IDs of the messages page found, in its order.
func ids(page search.Page) []uuid.UUID {
	out := []uuid.UUID{}
	for _, f := range page.Found {
		out = append(out, f.Message.ID)
	}
	return out
}

// request returns a search for text on the first page of 20.
func request(text string) search.Request {
	return search.Request{Text: text, Page: 1, Limit: 20}
}

// A search shows each conversation as storage holds it, through its
// message, in the index's order, with where the query matched the
// message's subject and its body text.
func TestSearch(t *testing.T) {
	s := newSearchStack()
	first := s.add(t, asha, ashaWork, "Your invoice", "Hello Asha, please pay the invoice today.", s.inbox)
	second := s.add(t, asha, ashaHome, "Receipt", "The invoice is paid.", s.inbox)

	page, err := s.searcher.Search(t.Context(), asha, request("invoice"))
	require.NoError(t, err)

	assert.Equal(t, search.Page{Total: 2, Found: []search.Found{
		{
			Conversation:   *first.conversation,
			Message:        first.message,
			SubjectMatches: []search.Span{{Start: len("Your "), Length: len("invoice")}},
			Preview:        "…please pay the invoice today.",
			PreviewMatches: []search.Span{{Start: len("…please pay the "), Length: len("invoice")}},
		},
		{
			Conversation:   *second.conversation,
			Message:        second.message,
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
	assert.Equal(t, m.message.Preview, page.Found[0].Preview)
	assert.Empty(t, page.Found[0].PreviewMatches)
}

// Without places, a search looks in All mail: Spam, Trash, and drafts are
// left out, whether the index knows a message is there or only storage
// does. A message moved to Trash since the index saw it still counts in
// the total until the index catches up.
func TestSearchHidden(t *testing.T) {
	s := newSearchStack()
	spam := s.memory.mailbox(asha, mailsync.RoleJunk)
	trash := s.memory.mailbox(asha, mailsync.RoleTrash)
	drafts := s.memory.mailbox(asha, mailsync.RoleDrafts)
	shown := s.add(t, asha, ashaWork, "Invoice", "", s.inbox)
	s.add(t, asha, ashaWork, "Invoice", "", s.inbox, spam)
	s.add(t, asha, ashaWork, "Invoice", "", trash)
	s.add(t, asha, ashaWork, "Invoice", "", drafts)
	s.add(t, asha, ashaWork, "Invoice", "", s.inbox)
	s.memory.messages[asha][len(s.memory.messages[asha])-1].mailboxes = []uuid.UUID{trash}

	page, err := s.searcher.Search(t.Context(), asha, request("invoice"))
	require.NoError(t, err)

	assert.Equal(t, []uuid.UUID{shown.message.ID}, ids(page))
	assert.Equal(t, 2, page.Total)
}

// Each place finds the conversations its list shows: by the
// conversation's places, through a message neither in Spam, Trash, nor
// drafts, or for Spam and Trash any message but a draft.
func TestSearchPlaces(t *testing.T) {
	s := newSearchStack()
	sent := s.memory.mailbox(asha, mailsync.RoleSent)
	spam := s.memory.mailbox(asha, mailsync.RoleJunk)
	trash := s.memory.mailbox(asha, mailsync.RoleTrash)
	s.memory.mailbox(asha, mailsync.RoleDrafts)
	clients := s.memory.label(asha)
	family := s.memory.label(asha)

	inbox := s.add(t, asha, ashaWork, "Invoice", "", s.inbox)
	// Archived itself, but a reply put its conversation in the inbox, and
	// it is labelled Clients and starred.
	archived := s.add(t, asha, ashaWork, "Invoice", "")
	archived = s.place(t, asha, archived, placement{mailboxes: []uuid.UUID{s.inbox, clients}, allMail: true, starred: true})
	sentOne := s.add(t, asha, ashaWork, "Invoice", "", sent)
	spamOne := s.add(t, asha, ashaWork, "Invoice", "", spam)
	trashOne := s.add(t, asha, ashaWork, "Invoice", "", trash)
	// In Trash itself, in a conversation also in the inbox: found in Trash,
	// not in the inbox.
	trashedReply := s.add(t, asha, ashaWork, "Invoice", "", trash)
	trashedReply = s.place(t, asha, trashedReply, placement{mailboxes: []uuid.UUID{s.inbox, trash}, allMail: true})
	familyOne := s.add(t, asha, ashaHome, "Invoice", "", family)

	cases := map[string]struct {
		places []conversation.Place
		want   []uuid.UUID
	}{
		"none":    {nil, []uuid.UUID{inbox.message.ID, archived.message.ID, sentOne.message.ID, familyOne.message.ID}},
		"all":     {[]conversation.Place{{View: conversation.ViewAll}}, []uuid.UUID{inbox.message.ID, archived.message.ID, sentOne.message.ID, familyOne.message.ID}},
		"inbox":   {[]conversation.Place{{View: conversation.ViewInbox}}, []uuid.UUID{inbox.message.ID, archived.message.ID}},
		"starred": {[]conversation.Place{{View: conversation.ViewStarred}}, []uuid.UUID{archived.message.ID}},
		"sent":    {[]conversation.Place{{View: conversation.ViewSent}}, []uuid.UUID{sentOne.message.ID}},
		"spam":    {[]conversation.Place{{View: conversation.ViewSpam}}, []uuid.UUID{spamOne.message.ID}},
		"trash":   {[]conversation.Place{{View: conversation.ViewTrash}}, []uuid.UUID{trashOne.message.ID, trashedReply.message.ID}},
		"label":   {[]conversation.Place{{Mailbox: clients}}, []uuid.UUID{archived.message.ID}},
		"any of":  {[]conversation.Place{{Mailbox: family}, {View: conversation.ViewSpam}}, []uuid.UUID{familyOne.message.ID, spamOne.message.ID}},
		"everywhere": {
			[]conversation.Place{{View: conversation.ViewAll}, {View: conversation.ViewSpam}, {View: conversation.ViewTrash}},
			[]uuid.UUID{inbox.message.ID, archived.message.ID, sentOne.message.ID, spamOne.message.ID, trashOne.message.ID, trashedReply.message.ID, familyOne.message.ID},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := request("invoice")
			r.Places = c.places

			page, err := s.searcher.Search(t.Context(), asha, r)
			require.NoError(t, err)

			assert.ElementsMatch(t, c.want, ids(page))
			assert.Equal(t, len(c.want), page.Total)
		})
	}
}

// Storage decides the places: a conversation that left a place since the
// index saw it is left out, though the index still counts it.
func TestSearchPlaceLeftSince(t *testing.T) {
	s := newSearchStack()
	clients := s.memory.label(asha)
	kept := s.add(t, asha, ashaWork, "Invoice", "", clients)
	left := s.add(t, asha, ashaWork, "Invoice", "", clients)
	for n, m := range s.memory.messages[asha] {
		if m.message.ID == left.message.ID {
			c := *m.conversation
			c.LabelIDs = []uuid.UUID{}
			s.memory.messages[asha][n].conversation = &c
		}
	}
	r := request("invoice")
	r.Places = []conversation.Place{{Mailbox: clients}}

	page, err := s.searcher.Search(t.Context(), asha, r)
	require.NoError(t, err)

	assert.Equal(t, []uuid.UUID{kept.message.ID}, ids(page))
	assert.Equal(t, 2, page.Total)
}

// A label the member does not have, another member's included, is
// refused before the index is asked.
func TestSearchLabelNotFound(t *testing.T) {
	s := newSearchStack()
	mine := s.memory.label(asha)
	theirs := s.memory.label(budi)
	s.add(t, asha, ashaWork, "Invoice", "", mine)
	s.index.Fail = errors.New("must not be searched")

	for name, label := range map[string]uuid.UUID{"another member's": theirs, "missing": uuid.New(), "a role's": s.inbox} {
		t.Run(name, func(t *testing.T) {
			r := request("invoice")
			r.Places = []conversation.Place{{Mailbox: mine}, {Mailbox: label}}

			_, err := s.searcher.Search(t.Context(), asha, r)

			require.ErrorIs(t, err, conversation.ErrLabelNotFound)
		})
	}
}

// A view whose mailbox the member does not have, e.g. Spam before any
// account is connected, holds nothing, and asks the index nothing.
func TestSearchPlaceWithoutMailbox(t *testing.T) {
	s := newSearchStack()
	s.add(t, asha, ashaWork, "Invoice", "", s.inbox)
	s.index.Fail = errors.New("must not be searched")
	r := request("invoice")
	r.Places = []conversation.Place{{View: conversation.ViewSpam}, {View: conversation.ViewSent}}

	page, err := s.searcher.Search(t.Context(), asha, r)
	require.NoError(t, err)

	assert.Equal(t, search.Page{Found: []search.Found{}}, page)
}

// A search the index finds nothing for reads no messages from storage.
func TestSearchNothingFound(t *testing.T) {
	s := newSearchStack()
	s.add(t, asha, ashaWork, "Receipt", "", s.inbox)

	page, err := s.searcher.Search(t.Context(), asha, request("invoice"))
	require.NoError(t, err)

	assert.Equal(t, search.Page{Found: []search.Found{}}, page)
	assert.Zero(t, s.memory.reads)
}

// A message deleted since the index saw it is left out, and so is one
// whose conversation storage no longer has.
func TestSearchDeletedSince(t *testing.T) {
	s := newSearchStack()
	kept := s.add(t, asha, ashaWork, "Invoice", "", s.inbox)
	s.add(t, asha, ashaWork, "Invoice", "", s.inbox)
	s.add(t, asha, ashaWork, "Invoice", "", s.inbox)
	s.memory.messages[asha] = s.memory.messages[asha][:2]
	s.memory.messages[asha][1].conversation = nil

	page, err := s.searcher.Search(t.Context(), asha, request("invoice"))
	require.NoError(t, err)

	assert.Equal(t, []uuid.UUID{kept.message.ID}, ids(page))
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

	assert.Equal(t, []uuid.UUID{work.message.ID}, ids(page))
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
	assert.Equal(t, m.message.ID, page.Found[0].Message.ID)
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
	kept := s.add(t, asha, ashaWork, "Invoice", "", s.inbox).message
	other := s.add(t, asha, ashaWork, "Invoice", "", s.inbox).message
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

			assert.ElementsMatch(t, c.want, ids(page))
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
