// Package search finds a member's messages through a derived index: a
// searchable copy of message data that returns message IDs, while the
// messages themselves load from PostgreSQL (ADR005).
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
	InternalDate       time.Time // when the provider received it
	Subject            string
	From               string   // name and address, e.g. "Asha K <asha.k@example.co.id>"
	To                 []string // each name and address
	CC                 []string // each name and address
	BodyText           string   // the text of the bodies a view shows; empty until the body is stored
	FromAddress        string   // the sender's address alone
	// The text of the message's other text parts, e.g. the plain-text
	// alternative of HTML a view shows: found, but ranked below
	// [Document.BodyText].
	AltText string
	// The mailboxes the message is in, by inbok's IDs, Spam and Trash
	// included: the index holds every message, and a query decides which
	// mailboxes it leaves out.
	MailboxIDs []uuid.UUID
}

// Query is what a member searches for. The owner is not part of it: every
// [Index.Search] takes the owner as its own argument.
type Query struct {
	Text     string  // the member's words; empty matches every message
	Accounts []int64 // connected accounts to search; empty: all of the owner's
	// Mailboxes whose messages to leave out, e.g. the owner's Spam and
	// Trash: a message in any of them is left out.
	ExcludeMailboxes []uuid.UUID
	Page             int // from 1
	PageSize         int // at least 1
}

// Result is one page of a search.
type Result struct {
	IDs   []uuid.UUID // the page's messages, most relevant first
	Total int         // matches over every page, at most [MaxTotal]
}

// MaxTotal is the most matches a search counts or pages through. Meilisearch's
// default; its docs warn that a higher limit slows every search.
const MaxTotal = 1000

// Errors for a search the index refuses before running it.
var (
	ErrNoOwner = errors.New("search: no owner")
	ErrBadPage = errors.New("search: page and page size must be at least 1")
)

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

	// Search returns one page of owner's messages that match query, and never
	// another member's, even for another member's account ID. It returns
	// [ErrNoOwner] or [ErrBadPage], from [ValidateQuery], before searching.
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
// runs one. It returns [ErrNoOwner] from [ValidateOwner], and [ErrBadPage]
// when the page or the page size is below 1.
func ValidateQuery(owner int64, query Query) error {
	if err := ValidateOwner(owner); err != nil {
		return err
	}
	if query.Page < 1 || query.PageSize < 1 {
		return ErrBadPage
	}
	return nil
}
