// Package searchtest holds what tests of code that uses a [search.Index]
// need: [Fake], an index in memory, and [RunContract], the tests every
// index passes, so the fake behaves as the real one does.
package searchtest

import (
	"context"
	"slices"
	"strings"
	"sync"

	"uuid"

	"inbok/internal/search"
)

// Fake is a [search.Index] in memory. It matches text more simply than a
// real index: a document matches when each word of the query appears,
// ignoring case, in its subject, sender, recipients, or body text; there
// are no typos, prefixes, phrases, or exclusions. It returns matches in
// the order they were first added. The zero value is empty and ready. It
// is safe for concurrent use.
type Fake struct {
	// Fail, when set, is the error every method returns without doing
	// anything.
	Fail error

	mu        sync.Mutex
	documents []search.Document
}

// Document returns owner's document with id, and whether the fake has one.
func (f *Fake) Document(owner int64, id uuid.UUID) (search.Document, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.find(owner, id)
	if i < 0 {
		return search.Document{}, false
	}
	return clone(f.documents[i]), true
}

// Prepare does nothing: the fake has no settings.
func (f *Fake) Prepare(ctx context.Context) error {
	return f.refuse(ctx)
}

// Upsert adds documents, replacing whole any of the same owner with the
// same ID.
func (f *Fake) Upsert(ctx context.Context, documents []search.Document) error {
	if err := search.ValidateDocuments(documents); err != nil {
		return err
	}
	if err := f.refuse(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, document := range documents {
		document = clone(document)
		if i := f.find(document.OwnerUserID, document.ID); i >= 0 {
			f.documents[i] = document
			continue
		}
		f.documents = append(f.documents, document)
	}
	return nil
}

// Delete removes owner's documents with these IDs.
func (f *Fake) Delete(ctx context.Context, owner int64, ids []uuid.UUID) error {
	if err := search.ValidateOwner(owner); err != nil {
		return err
	}
	if err := f.refuse(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.documents = slices.DeleteFunc(f.documents, func(d search.Document) bool {
		return d.OwnerUserID == owner && slices.Contains(ids, d.ID)
	})
	return nil
}

// DeleteAccount removes every document of owner's connected account.
func (f *Fake) DeleteAccount(ctx context.Context, owner, accountID int64) error {
	if err := search.ValidateOwner(owner); err != nil {
		return err
	}
	if err := f.refuse(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.documents = slices.DeleteFunc(f.documents, func(d search.Document) bool {
		return d.OwnerUserID == owner && d.ConnectedAccountID == accountID
	})
	return nil
}

// Search returns one page of owner's documents that match query.
func (f *Fake) Search(ctx context.Context, owner int64, query search.Query) (search.Result, error) {
	if err := search.ValidateQuery(owner, query); err != nil {
		return search.Result{}, err
	}
	if err := f.refuse(ctx); err != nil {
		return search.Result{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	var matches []uuid.UUID
	for _, document := range f.documents {
		if matchesFilters(document, owner, query) && matchesText(document, query.Text) {
			matches = append(matches, document.ID)
		}
	}
	matches = matches[:min(len(matches), search.MaxTotal)]

	// Past the last page, start at the end: multiplying first could
	// overflow for a far page.
	start := len(matches)
	if query.Page-1 <= len(matches)/query.PageSize {
		start = (query.Page - 1) * query.PageSize
	}
	end := start + min(query.PageSize, len(matches)-start)
	return search.Result{IDs: slices.Clone(matches[start:end]), Total: len(matches)}, nil
}

// refuse returns [Fake.Fail] when it is set, or ctx's error when ctx is
// done, as a real index gives up on a caller that did.
func (f *Fake) refuse(ctx context.Context) error {
	if f.Fail != nil {
		return f.Fail
	}
	return ctx.Err()
}

// clone returns a copy of document that shares no slice with it, so a
// caller's later change to its slices changes nothing the fake holds.
func clone(document search.Document) search.Document {
	document.To = slices.Clone(document.To)
	document.CC = slices.Clone(document.CC)
	document.MailboxIDs = slices.Clone(document.MailboxIDs)
	return document
}

// find returns the index of owner's document with id, or -1: two members'
// documents never replace each other, as their indexes are apart.
func (f *Fake) find(owner int64, id uuid.UUID) int {
	return slices.IndexFunc(f.documents, func(d search.Document) bool {
		return d.OwnerUserID == owner && d.ID == id
	})
}

// matchesFilters reports whether document is owner's, in one of query's
// accounts when it names any, and in none of its excluded mailboxes.
func matchesFilters(document search.Document, owner int64, query search.Query) bool {
	if document.OwnerUserID != owner {
		return false
	}
	if len(query.Accounts) > 0 && !slices.Contains(query.Accounts, document.ConnectedAccountID) {
		return false
	}
	return !slices.ContainsFunc(document.MailboxIDs, func(id uuid.UUID) bool {
		return slices.Contains(query.ExcludeMailboxes, id)
	})
}

// matchesText reports whether each word of text appears, ignoring case, in
// document's searchable fields.
func matchesText(document search.Document, text string) bool {
	fields := []string{document.Subject, document.From, document.BodyText}
	fields = append(fields, document.To...)
	fields = append(fields, document.CC...)
	searchable := strings.ToLower(strings.Join(fields, "\n"))
	for word := range strings.FieldsSeq(strings.ToLower(text)) {
		if !strings.Contains(searchable, word) {
			return false
		}
	}
	return true
}
