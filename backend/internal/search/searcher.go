package search

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
	"unicode"
	"unicode/utf8"

	"uuid"

	"inbok/internal/mailsync"
)

// ErrAccountNotFound is a search of one connected account the member does
// not have.
var ErrAccountNotFound = errors.New("search: no such connected account")

// hiddenRoles are the roles of the mailboxes whose messages a search
// leaves out: Spam and Trash, as Gmail's search does, and drafts, which no
// view shows (ADR005).
var hiddenRoles = []mailsync.Role{mailsync.RoleJunk, mailsync.RoleTrash, mailsync.RoleDrafts}

// leadWords is how many words an excerpt keeps before its first match,
// inbok's choice: a result's preview is one line cut off at its end, so
// a match far into the excerpt would fall off a narrow screen.
const leadWords = 3

// Message is a message a search found, as storage holds it now.
type Message struct {
	ID        uuid.UUID
	AccountID int64
	Date      time.Time // when the provider received it
	From      []mailsync.Address
	Subject   string
	Preview   string // the provider's plain-text excerpt of its body, e.g. Gmail's snippet
	Unread    bool
	Starred   bool
}

// SearchStorage reads what a search shows, and what it leaves out.
type SearchStorage interface {
	// OwnsAccount reports whether member owner has connected account
	// accountID.
	OwnsAccount(ctx context.Context, owner, accountID int64) (bool, error)

	// MailboxIDs returns the IDs of owner's mailboxes with any of roles,
	// in any account, in any order.
	MailboxIDs(ctx context.Context, owner int64, roles []mailsync.Role) ([]uuid.UUID, error)

	// FoundMessages returns owner's messages among ids that are in no
	// mailbox with any of hidden's roles, in any order. An ID owner has no
	// such message with, e.g. one deleted or moved to Trash since, is left
	// out.
	FoundMessages(ctx context.Context, owner int64, ids []uuid.UUID, hidden []mailsync.Role) ([]Message, error)
}

// Request is what a member searches their mail for.
type Request struct {
	Text    string // the member's words
	Account int64  // a connected account to search alone; 0 searches every one
	Page    int    // from 1
	Limit   int    // results per page, at least 1
}

// Found is one result of a search: a conversation's best-matching message,
// and where the query matched what a result shows of it.
type Found struct {
	Message Message
	// SubjectMatches count in [Message.Subject]. Like PreviewMatches, they
	// lie inside their text, start and end on a character, and are sorted
	// without overlapping.
	SubjectMatches []Span
	// Preview is the message's body text around its best match, or when
	// the query matched no body text, [Message.Preview].
	Preview        string
	PreviewMatches []Span
}

// Page is one page of a search's results.
type Page struct {
	Found []Found // most relevant first
	// Total counts the matching conversations over every page, at most
	// [MaxTotal]: a page can hold fewer than its limit, as a message the
	// index still holds may have left storage, or Spam or Trash, since.
	Total int
}

// Searcher searches members' mail through the index, and shows what
// storage holds.
type Searcher struct {
	index   Index
	storage SearchStorage
}

// NewSearcher returns a searcher that finds messages in index and reads
// them from storage.
func NewSearcher(index Index, storage SearchStorage) *Searcher {
	return &Searcher{index: index, storage: storage}
}

// Search returns one page of owner's conversations whose messages match
// request, each by its best match, leaving out Spam, Trash, and drafts.
// It returns [ErrNoOwner] or [ErrBadPage] before reading anything,
// [ErrAccountNotFound] for an account owner does not have, and an error
// wrapping [ErrUnavailable] when the index does not answer.
//
// DESIGN: the index may lag behind storage (ADR005), so storage decides
// what is shown: the index leaves out messages in Spam, Trash, or drafts
// as it last saw them, and storage drops those there now.
func (s *Searcher) Search(ctx context.Context, owner int64, request Request) (Page, error) {
	query := Query{Text: request.Text, Page: request.Page, PageSize: request.Limit}
	if err := ValidateQuery(owner, query); err != nil {
		return Page{}, err
	}

	// 1. Check the account is the owner's, so a search of one they no
	// longer have says so rather than finding nothing.
	if request.Account != 0 {
		owns, err := s.storage.OwnsAccount(ctx, owner, request.Account)
		if err != nil {
			return Page{}, fmt.Errorf("check account: %w", err)
		}
		if !owns {
			return Page{}, ErrAccountNotFound
		}
		query.Accounts = []int64{request.Account}
	}

	// 2. Search, leaving out the hidden mailboxes' messages.
	hidden, err := s.storage.MailboxIDs(ctx, owner, hiddenRoles)
	if err != nil {
		return Page{}, fmt.Errorf("list hidden mailboxes: %w", err)
	}
	query.ExcludeMailboxes = hidden
	result, err := s.index.Search(ctx, owner, query)
	if err != nil {
		return Page{}, err
	}

	if len(result.Hits) == 0 {
		return Page{Found: []Found{}, Total: result.Total}, nil
	}

	// 3. Read the messages found as storage holds them now.
	ids := make([]uuid.UUID, len(result.Hits))
	for n, hit := range result.Hits {
		ids[n] = hit.ID
	}
	messages, err := s.storage.FoundMessages(ctx, owner, ids, hiddenRoles)
	if err != nil {
		return Page{}, fmt.Errorf("read found messages: %w", err)
	}
	byID := make(map[uuid.UUID]Message, len(messages))
	for _, m := range messages {
		byID[m.ID] = m
	}

	page := Page{Found: make([]Found, 0, len(messages)), Total: result.Total}
	for _, hit := range result.Hits {
		if m, ok := byID[hit.ID]; ok {
			page.Found = append(page.Found, found(m, hit))
		}
	}
	return page, nil
}

// found returns message m as a result, with hit's matches on the text it
// shows.
func found(m Message, hit Hit) Found {
	result := Found{Message: m, Preview: m.Preview}
	// The index's subject is storage's unless the message changed since,
	// and matches in another text would mark the wrong words.
	if hit.Subject == m.Subject {
		result.SubjectMatches = within(m.Subject, hit.SubjectMatches)
	}
	if matches := within(hit.Excerpt, hit.ExcerptMatches); len(matches) > 0 {
		result.Preview, result.PreviewMatches = lead(hit.Excerpt, matches)
	}
	return result
}

// within returns the spans that lie inside text and start and end on a
// character's first byte, so none marks part of a character or past the
// end, sorted, with overlapping ones merged.
func within(text string, spans []Span) []Span {
	var kept []Span
	for _, s := range spans {
		end := s.Start + s.Length
		if s.Start < 0 || s.Length < 1 || end > len(text) {
			continue
		}
		if !utf8.RuneStart(text[s.Start]) || (end < len(text) && !utf8.RuneStart(text[end])) {
			continue
		}
		kept = append(kept, s)
	}
	slices.SortFunc(kept, func(a, b Span) int { return a.Start - b.Start })
	// Marks completed to their word's end can overlap, e.g. in a script
	// written without spaces.
	var out []Span
	for _, s := range kept {
		if n := len(out); n > 0 && s.Start < out[n-1].Start+out[n-1].Length {
			out[n-1].Length = max(out[n-1].Length, s.Start+s.Length-out[n-1].Start)
			continue
		}
		out = append(out, s)
	}
	return out
}

// lead returns excerpt cut to start at most [leadWords] words before its
// first match, marked with [ExcerptCut] where cut, and matches moved to
// count in the cut text. matches is not empty, and sorted.
func lead(excerpt string, matches []Span) (string, []Span) {
	cut := matches[0].Start
	for range leadWords {
		start := wordBefore(excerpt, cut)
		if start == cut {
			break
		}
		cut = start
	}
	if cut == 0 {
		return excerpt, matches
	}
	shift := len(ExcerptCut) - cut
	moved := make([]Span, len(matches))
	for n, m := range matches {
		moved[n] = Span{Start: m.Start + shift, Length: m.Length}
	}
	return ExcerptCut + excerpt[cut:], moved
}

// wordBefore returns where the word before byte at of text starts, past
// any space between, or at itself when no word comes before it.
func wordBefore(text string, at int) int {
	i := at
	// 1. Step back over the space before at, so step 2 reads a word.
	for i > 0 {
		r, size := utf8.DecodeLastRuneInString(text[:i])
		if !unicode.IsSpace(r) {
			break
		}
		i -= size
	}
	if i == 0 {
		return at
	}
	// 2. Step back to the word's first character.
	for i > 0 {
		r, size := utf8.DecodeLastRuneInString(text[:i])
		if unicode.IsSpace(r) {
			break
		}
		i -= size
	}
	return i
}
