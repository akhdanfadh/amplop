// Package search finds a member's messages through a derived index: a
// searchable copy of message data that returns message IDs, while the
// messages themselves load from PostgreSQL.
package search

import (
	"context"
	"errors"
	"time"

	"uuid"
)

// Document is one message as the index holds it: the text a search matches
// and the facts it filters on.
type Document struct {
	ID                 uuid.UUID // the message
	OwnerUserID        int64     // the member whose mail it is
	ConnectedAccountID int64     // the account it synchronized from
	ThreadID           string    // the provider's thread; with the account, the message's conversation
	InternalDate       time.Time // when the provider received it
	Subject            string
	From               string   // name and address, e.g. "Asha K <asha.k@example.net>"
	To                 []string // each name and address
	CC                 []string // each name and address
	BodyText           string   // the text of the bodies a view shows; empty until the body is stored
	// FromAddresses are the addresses alone of the message's From, in its
	// order, as storage holds them, without empty ones.
	FromAddresses []string
	// HasAttachment is whether a view offers any of the message's parts as
	// a download, as [messagecontent.MessageTexts.HasAttachment] says;
	// false until the body is stored.
	HasAttachment bool
	// The text of the message's other text parts, e.g. the plain-text
	// alternative of HTML a view shows: found, but ranked below
	// [Document.BodyText].
	AltText string
	// The mailboxes the message is in, by amplop's IDs, Spam and Trash
	// included: the index holds every message, and a query decides which
	// mailboxes it leaves out.
	MailboxIDs []uuid.UUID
	// ConversationMailboxIDs are the mailboxes the message's conversation
	// is in, as lists place it, [conversation.Summary.Mailboxes]: a reply
	// in the inbox puts every message of its conversation there. Empty for
	// a message with no conversation, e.g. a thread's only draft.
	ConversationMailboxIDs []uuid.UUID
	// ConversationFlagged is whether the message's conversation is
	// starred, which any of its messages makes it.
	ConversationFlagged bool
}

// Scope is one place a search looks in, as the index tells places apart.
// A message is in it when its conversation is in any of InConversation, or
// any conversation when that is empty; is starred, when Flagged; and is
// itself in none of Exclude.
type Scope struct {
	InConversation []uuid.UUID
	Flagged        bool
	Exclude        []uuid.UUID
}

// Query is what a member searches for. The owner is not part of it: every
// [Index.Search] takes the owner as its own argument.
type Query struct {
	Text     string  // the member's words; empty matches every message
	Accounts []int64 // connected accounts to search; empty: all of the owner's
	// Scopes keeps messages in any of them; empty: every message.
	Scopes []Scope
	// From keeps messages whose From holds any of these addresses,
	// ignoring case as the index compares text; empty: any sender.
	From []string
	// Since and Before keep messages the provider received at or after
	// Since and before Before; a zero time leaves its end open.
	Since  time.Time
	Before time.Time
	// WithAttachment keeps only messages with an attachment, as
	// [Document.HasAttachment] says.
	WithAttachment bool
	Page           int // from 1
	PageSize       int // at least 1
}

// Result is one page of a search.
type Result struct {
	Hits  []Hit // the page's messages, most relevant first
	Total int   // matching conversations over every page, at most [MaxTotal]
}

// Hit is a message a search found: its conversation's best match, and
// where in its text the query matched. A match covers the word matched,
// or the typed start of a longer word, as a search-as-you-type prefix. An
// index may return matches that overlap or leave their text; the
// [Searcher] drops those that leave it and merges those that overlap.
type Hit struct {
	ID uuid.UUID
	// Subject is the message's subject as the index holds it, which
	// SubjectMatches count in.
	Subject        string
	SubjectMatches []Span
	// Excerpt is the part of the message's body text around its best
	// match, perhaps cut short at either end, which ExcerptMatches count
	// in; empty when the query matched no body text.
	Excerpt        string
	ExcerptMatches []Span
}

// ExcerptCut marks where a [Hit.Excerpt] was cut short.
const ExcerptCut = "\u2026" // the ellipsis

// Span is where a query matched a text: Length bytes from byte Start, of
// the text in UTF-8.
type Span struct {
	Start  int
	Length int
}

// MaxTotal is the most matches a search counts or pages through. Meilisearch's
// default; its docs warn that a higher limit slows every search.
const MaxTotal = 1000

// Errors for a search the index refuses before running it.
var (
	ErrNoOwner  = errors.New("search: no owner")
	ErrBadPage  = errors.New("search: page and page size must be at least 1")
	ErrBadRange = errors.New("search: the range's start is not before its end")
)

// ErrUnavailable is an index that did not answer, or answered that it
// failed, e.g. while it restarts: asking again later may succeed.
var ErrUnavailable = errors.New("search: index unavailable")

// Index is the derived search index. It keeps each member's documents
// apart: every method that names an owner reads or changes only that
// member's documents, whatever IDs it is given. Every method may be called
// again with the same input, e.g. by a job that retries, and leaves the
// index as one call would. Calls are not ordered against each other,
// though: an upsert repeated after a delete of the same message brings it
// back, so a caller that may repeat an older write must order its writes
// itself. A method returns once the index has applied the change, so a
// later search sees it, or returns ctx's error, wrapped, when the caller
// gives up first.
type Index interface {
	// Prepare applies the index's settings to the documents it already
	// holds, e.g. after a release changes them. Call it at startup, before
	// the first write.
	Prepare(ctx context.Context) error

	// Upsert adds documents, each under its own owner, replacing whole any
	// of that owner's with the same ID. It returns [ErrNoOwner], from
	// [ValidateDocuments], before writing any of them.
	Upsert(ctx context.Context, documents []Document) error

	// Delete removes owner's documents with these message IDs; an ID owner
	// has no document for is not an error. It returns [ErrNoOwner], from
	// [ValidateOwner], before deleting.
	Delete(ctx context.Context, owner int64, ids []uuid.UUID) error

	// DeleteAccount removes every document of owner's connected account. It
	// returns [ErrNoOwner], from [ValidateOwner], before deleting.
	DeleteAccount(ctx context.Context, owner, accountID int64) error

	// Search returns one page of owner's messages that match every word of
	// query and all of its filters, and never another member's, even for
	// another member's account ID: at most one message per conversation,
	// its best match among those, or with no words its newest. Without
	// words, results come newest first, and hits carry no matches. It
	// returns [ErrNoOwner], [ErrBadPage], or [ErrBadRange], from
	// [ValidateQuery], before searching, and an error wrapping
	// [ErrUnavailable] when the index does not answer.
	Search(ctx context.Context, owner int64, query Query) (Result, error)

	// Owners returns, in no set order, every owner the index keeps
	// documents for: each owner written to since [Index.DeleteOwner] last
	// removed them, even one whose documents were all deleted since.
	Owners(ctx context.Context) ([]int64, error)

	// DocumentIDs returns the message IDs of all of owner's documents, in
	// no set order, and none for an owner the index keeps nothing for. It
	// returns [ErrNoOwner], from [ValidateOwner], before reading.
	DocumentIDs(ctx context.Context, owner int64) ([]uuid.UUID, error)

	// DeleteOwner removes owner and all of their documents, so
	// [Index.Owners] no longer returns them; an owner the index keeps
	// nothing for is not an error. It returns [ErrNoOwner], from
	// [ValidateOwner], before deleting.
	DeleteOwner(ctx context.Context, owner int64) error
}

// ValidateOwner holds an owner to the rule every [Index] checks before it
// uses one. It returns [ErrNoOwner] when owner is not a user ID.
func ValidateOwner(owner int64) error {
	// User IDs start at 1 (an identity column), so 0 is an owner left unset.
	if owner < 1 {
		return ErrNoOwner
	}
	return nil
}

// ValidateDocuments holds documents to the rule every [Index] checks
// before it writes them. It returns [ErrNoOwner] when any document's
// owner is not a user ID.
func ValidateDocuments(documents []Document) error {
	for _, document := range documents {
		if err := ValidateOwner(document.OwnerUserID); err != nil {
			return err
		}
	}
	return nil
}

// ValidateQuery holds a search to the rules every [Index] checks before it
// runs one. It returns [ErrNoOwner] from [ValidateOwner], [ErrBadPage]
// when the page or the page size is below 1, and [ErrBadRange] when
// [Query.Since] is not before [Query.Before], a range nothing is in.
func ValidateQuery(owner int64, query Query) error {
	if err := ValidateOwner(owner); err != nil {
		return err
	}
	if query.Page < 1 || query.PageSize < 1 {
		return ErrBadPage
	}
	if !query.Since.IsZero() && !query.Before.IsZero() && !query.Since.Before(query.Before) {
		return ErrBadRange
	}
	return nil
}
