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

	"inbok/internal/conversation"
	"inbok/internal/mailsync"
)

// ErrAccountNotFound is a search of one connected account the member does
// not have.
var ErrAccountNotFound = errors.New("search: no such connected account")

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
}

// Stored is a message a search found, as storage holds it now, with what
// decides whether a place shows it: the roles of the mailboxes it is in,
// and its conversation as lists show it.
type Stored struct {
	Message      Message
	Roles        []mailsync.Role
	Conversation conversation.Conversation
}

// SearchStorage reads what a search shows, and where it looks.
type SearchStorage interface {
	// OwnsAccount reports whether member owner has connected account
	// accountID.
	OwnsAccount(ctx context.Context, owner, accountID int64) (bool, error)

	// RoleMailboxes returns the IDs of owner's mailboxes that have a role,
	// in every account, by role.
	RoleMailboxes(ctx context.Context, owner int64) (map[mailsync.Role][]uuid.UUID, error)

	// LabelIDs returns those of ids that are owner's labels, mailboxes of
	// theirs with no role that they made, in any order.
	LabelIDs(ctx context.Context, owner int64, ids []uuid.UUID) ([]uuid.UUID, error)

	// FoundMessages returns owner's messages among ids, each with its
	// conversation, in any order. An ID owner has no such message with,
	// e.g. one deleted since, or a draft alone in its thread, is left out.
	FoundMessages(ctx context.Context, owner int64, ids []uuid.UUID) ([]Stored, error)
}

// Request is what a member searches their mail for.
type Request struct {
	Text    string // the member's words; empty finds every message the filters keep
	Account int64  // a connected account to search alone; 0 searches every one
	// Places are where to search: a conversation in any of them matches.
	// Empty searches All mail.
	Places []conversation.Place
	// From, Since, Before, and WithAttachment filter as [Query]'s fields
	// of those names do.
	From           []string
	Since          time.Time
	Before         time.Time
	WithAttachment bool
	Page           int // from 1
	Limit          int // results per page, at least 1
}

// Found is one result of a search: a conversation, its best-matching
// message, and where the query matched what a result shows of it.
type Found struct {
	Conversation conversation.Conversation
	// Message is the conversation's best match, or with no words its
	// newest, which passes every filter itself.
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
	// index still holds may have left storage, or its places, since.
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

// Search returns one page of owner's conversations in any of request's
// places whose messages match request, each by its best match, or with no
// words its newest. A place shows a conversation as its list does (ADR031),
// through a message in neither Spam, Trash, nor drafts, or for Spam and
// Trash any message but a draft. It returns [ErrNoOwner], [ErrBadPage], or
// [ErrBadRange] before reading anything, [ErrAccountNotFound] for an
// account owner does not have, [conversation.ErrLabelNotFound] for a label
// owner does not have, and an error wrapping [ErrUnavailable] when the
// index does not answer.
//
// DESIGN: the index may lag behind storage (ADR005), so storage decides
// what is shown: the index finds messages in the places it last saw, and
// storage drops those no longer there.
func (s *Searcher) Search(ctx context.Context, owner int64, request Request) (Page, error) {
	query := Query{
		Text:           request.Text,
		From:           request.From,
		Since:          request.Since,
		Before:         request.Before,
		WithAttachment: request.WithAttachment,
		Page:           request.Page,
		PageSize:       request.Limit,
	}
	if err := ValidateQuery(owner, query); err != nil {
		return Page{}, err
	}
	places := request.Places
	if len(places) == 0 {
		places = []conversation.Place{{View: conversation.ViewAll}}
	}

	// 1. Check the account and labels are the owner's, so a search of one
	// they no longer have says so rather than finding nothing.
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
	if err := s.checkLabels(ctx, owner, places); err != nil {
		return Page{}, err
	}

	// 2. Search the places as the index tells them apart. Places with no
	// mailbox to be in, e.g. Spam before any account is connected, hold
	// nothing.
	roles, err := s.storage.RoleMailboxes(ctx, owner)
	if err != nil {
		return Page{}, fmt.Errorf("list role mailboxes: %w", err)
	}
	query.Scopes = scopes(places, roles)
	if len(query.Scopes) == 0 {
		return Page{Found: []Found{}}, nil
	}
	result, err := s.index.Search(ctx, owner, query)
	if err != nil {
		return Page{}, err
	}

	if len(result.Hits) == 0 {
		return Page{Found: []Found{}, Total: result.Total}, nil
	}

	// 3. Read the messages found as storage holds them now, and keep those
	// still in the places.
	ids := make([]uuid.UUID, len(result.Hits))
	for n, hit := range result.Hits {
		ids[n] = hit.ID
	}
	stored, err := s.storage.FoundMessages(ctx, owner, ids)
	if err != nil {
		return Page{}, fmt.Errorf("read found messages: %w", err)
	}
	byID := make(map[uuid.UUID]Stored, len(stored))
	for _, m := range stored {
		byID[m.Message.ID] = m
	}

	page := Page{Found: make([]Found, 0, len(stored)), Total: result.Total}
	for _, hit := range result.Hits {
		if m, ok := byID[hit.ID]; ok && shown(m, places) {
			page.Found = append(page.Found, found(m, hit))
		}
	}
	return page, nil
}

// checkLabels returns [conversation.ErrLabelNotFound] when a place among
// places is a label owner does not have.
func (s *Searcher) checkLabels(ctx context.Context, owner int64, places []conversation.Place) error {
	var wanted []uuid.UUID
	for _, place := range places {
		if place.View == "" {
			wanted = append(wanted, place.Mailbox)
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	labels, err := s.storage.LabelIDs(ctx, owner, wanted)
	if err != nil {
		return fmt.Errorf("check labels: %w", err)
	}
	for _, id := range wanted {
		if !slices.Contains(labels, id) {
			return conversation.ErrLabelNotFound
		}
	}
	return nil
}

// scopes returns places as the index tells them apart, given the owner's
// mailboxes by role, leaving out each view whose role no mailbox has.
func scopes(places []conversation.Place, roles map[mailsync.Role][]uuid.UUID) []Scope {
	drafts := roles[mailsync.RoleDrafts]
	hidden := slices.Concat(roles[mailsync.RoleJunk], roles[mailsync.RoleTrash], drafts)
	// The role whose mailboxes hold each view's conversations.
	viewRoles := map[conversation.View]mailsync.Role{
		conversation.ViewInbox: mailsync.RoleInbox,
		conversation.ViewSent:  mailsync.RoleSent,
		conversation.ViewSpam:  mailsync.RoleJunk,
		conversation.ViewTrash: mailsync.RoleTrash,
	}
	var out []Scope
	for _, place := range places {
		scope := Scope{Exclude: hidden}
		switch place.View {
		case conversation.ViewAll:
		case conversation.ViewStarred:
			scope.Flagged = true
		case "":
			scope.InConversation = []uuid.UUID{place.Mailbox}
		default:
			scope.InConversation = roles[viewRoles[place.View]]
			if len(scope.InConversation) == 0 {
				continue
			}
		}
		// Spam and Trash show their messages, which every other place
		// leaves out; drafts show nowhere (ADR001, "Conversations").
		if place.View == conversation.ViewSpam || place.View == conversation.ViewTrash {
			scope.Exclude = drafts
		}
		out = append(out, scope)
	}
	return out
}

// shown reports whether any of places shows message m, as [scopes] tells
// the index to find it.
func shown(m Stored, places []conversation.Place) bool {
	if slices.Contains(m.Roles, mailsync.RoleDrafts) {
		return false
	}
	hidden := slices.Contains(m.Roles, mailsync.RoleJunk) || slices.Contains(m.Roles, mailsync.RoleTrash)
	for _, place := range places {
		showsHidden := place.View == conversation.ViewSpam || place.View == conversation.ViewTrash
		if (showsHidden || !hidden) && m.Conversation.In(place) {
			return true
		}
	}
	return false
}

// found returns stored's conversation as a result, through its message,
// with hit's matches on the text it shows.
func found(stored Stored, hit Hit) Found {
	m := stored.Message
	result := Found{Conversation: stored.Conversation, Message: m, Preview: m.Preview}
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
