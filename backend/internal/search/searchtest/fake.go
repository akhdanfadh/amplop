// Package searchtest holds what tests of code that uses a [search.Index]
// need: [Fake], an index in memory, and [RunContract], the tests every
// index passes, so the fake behaves as the real one does.
package searchtest

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"uuid"

	"amplop/internal/search"
)

// Fake is a [search.Index] in memory. It matches text more simply than a
// real index: a document matches when each word of the query appears,
// ignoring case, in its subject, sender, recipients, or either body text;
// there are no typos, prefixes, phrases, or exclusions. It compares sender
// addresses as [strings.EqualFold] does, which agrees with Meilisearch on
// the cases [RunContract] tests, e.g. ASCII letters and Ü, but not on
// every one: Meilisearch also normalizes, e.g. a decomposed ü to ü, trims
// spaces around a value, and matches no value past about 490 bytes. It
// returns matches in the order they were first added, or with no words
// newest first, each conversation's newest, and marks every place a word
// appears: in the subject, and in the whole of the first body text it
// appears in, uncut. The zero value is empty and ready. It is safe for
// concurrent use.
type Fake struct {
	// Fail, when set, is the error every method returns without doing
	// anything.
	Fail error

	mu        sync.Mutex
	documents []search.Document
	// owners are those the fake keeps documents for, as a real index keeps
	// a member's until [Fake.DeleteOwner], even with all of them deleted.
	owners map[int64]bool
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
		if f.owners == nil {
			f.owners = map[int64]bool{}
		}
		f.owners[document.OwnerUserID] = true
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

	// Each conversation's newest match, in the order its conversation first
	// matched: Meilisearch's date tie-break picks the newest of equal ones.
	var matches []search.Hit
	newest := map[conversation]int{} // each conversation's place in matches
	dates := map[conversation]time.Time{}
	dateOf := map[uuid.UUID]time.Time{}
	for _, document := range f.documents {
		if !matchesFilters(document, owner, query) || !matchesText(document, query.Text) {
			continue
		}
		dateOf[document.ID] = document.InternalDate
		key := conversation{document.ConnectedAccountID, document.ThreadID}
		at, seen := newest[key]
		switch {
		case !seen:
			newest[key], dates[key] = len(matches), document.InternalDate
			matches = append(matches, hit(document, query.Text))
		case document.InternalDate.After(dates[key]):
			matches[at], dates[key] = hit(document, query.Text), document.InternalDate
		}
	}
	// With no words to rank by, the date orders every match, as
	// Meilisearch's last ranking rule does.
	if strings.TrimSpace(query.Text) == "" {
		slices.SortStableFunc(matches, func(a, b search.Hit) int {
			return dateOf[b.ID].Compare(dateOf[a.ID])
		})
	}
	matches = matches[:min(len(matches), search.MaxTotal)]

	// Past the last page, start at the end: multiplying first could
	// overflow for a far page.
	start := len(matches)
	if query.Page-1 <= len(matches)/query.PageSize {
		start = (query.Page - 1) * query.PageSize
	}
	end := start + min(query.PageSize, len(matches)-start)
	return search.Result{Hits: slices.Clone(matches[start:end]), Total: len(matches)}, nil
}

// Owners returns every owner written to since [Fake.DeleteOwner] last removed
// them.
func (f *Fake) Owners(ctx context.Context) ([]int64, error) {
	if err := f.refuse(ctx); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Collect(maps.Keys(f.owners)), nil
}

// DocumentIDs returns the IDs of owner's documents.
func (f *Fake) DocumentIDs(ctx context.Context, owner int64) ([]uuid.UUID, error) {
	if err := search.ValidateOwner(owner); err != nil {
		return nil, err
	}
	if err := f.refuse(ctx); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []uuid.UUID
	for _, document := range f.documents {
		if document.OwnerUserID == owner {
			ids = append(ids, document.ID)
		}
	}
	return ids, nil
}

// DeleteOwner removes owner and their documents.
func (f *Fake) DeleteOwner(ctx context.Context, owner int64) error {
	if err := search.ValidateOwner(owner); err != nil {
		return err
	}
	if err := f.refuse(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.owners, owner)
	f.documents = slices.DeleteFunc(f.documents, func(d search.Document) bool {
		return d.OwnerUserID == owner
	})
	return nil
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
	document.ConversationMailboxIDs = slices.Clone(document.ConversationMailboxIDs)
	document.FromAddresses = slices.Clone(document.FromAddresses)
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
// accounts when it names any, in one of its scopes when it names any, and
// passes its sender, date, and attachment filters.
func matchesFilters(document search.Document, owner int64, query search.Query) bool {
	if document.OwnerUserID != owner {
		return false
	}
	if len(query.Accounts) > 0 && !slices.Contains(query.Accounts, document.ConnectedAccountID) {
		return false
	}
	if len(query.Scopes) > 0 && !slices.ContainsFunc(query.Scopes, func(scope search.Scope) bool { return inScope(document, scope) }) {
		return false
	}
	if len(query.From) > 0 && !slices.ContainsFunc(document.FromAddresses, func(address string) bool {
		return slices.ContainsFunc(query.From, func(wanted string) bool { return strings.EqualFold(address, wanted) })
	}) {
		return false
	}
	if !query.Since.IsZero() && document.InternalDate.Before(query.Since) {
		return false
	}
	if !query.Before.IsZero() && !document.InternalDate.Before(query.Before) {
		return false
	}
	return !query.WithAttachment || document.HasAttachment
}

// inScope reports whether document is in scope.
func inScope(document search.Document, scope search.Scope) bool {
	if len(scope.InConversation) > 0 && !slices.ContainsFunc(document.ConversationMailboxIDs, func(id uuid.UUID) bool {
		return slices.Contains(scope.InConversation, id)
	}) {
		return false
	}
	if scope.Flagged && !document.ConversationFlagged {
		return false
	}
	return !slices.ContainsFunc(document.MailboxIDs, func(id uuid.UUID) bool { return slices.Contains(scope.Exclude, id) })
}

// matchesText reports whether each word of text appears, ignoring case, in
// document's searchable fields.
func matchesText(document search.Document, text string) bool {
	fields := []string{document.Subject, document.From, document.BodyText, document.AltText}
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

// conversation names a document's conversation: its account and thread.
type conversation struct {
	account int64
	thread  string
}

// hit returns document as a search for text finds it.
func hit(document search.Document, text string) search.Hit {
	words := strings.Fields(text)
	found := search.Hit{ID: document.ID, Subject: document.Subject, SubjectMatches: spans(document.Subject, words)}
	for _, body := range []string{document.BodyText, document.AltText} {
		if matches := spans(body, words); len(matches) > 0 {
			found.Excerpt, found.ExcerptMatches = body, matches
			break
		}
	}
	return found
}

// spans returns, in order, every place in text where one of words
// appears, ignoring case.
func spans(text string, words []string) []search.Span {
	var out []search.Span
	for start := range len(text) {
		for _, word := range words {
			end := start + len(word)
			if end <= len(text) && strings.EqualFold(text[start:end], word) {
				out = append(out, search.Span{Start: start, Length: len(word)})
				break
			}
		}
	}
	return out
}
