package meilisearch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"uuid"

	meili "github.com/meilisearch/meilisearch-go"

	"inbok/internal/search"
)

// hit is what a search returns of each document.
type hit struct {
	ID string `json:"id"`
}

// Search returns one page of owner's messages that match query, most
// relevant first, and no matches for a member with no index yet. It
// returns [search.ErrNoOwner] or [search.ErrBadPage] before searching.
func (i *Index) Search(ctx context.Context, owner int64, query search.Query) (search.Result, error) {
	if err := search.ValidateQuery(owner, query); err != nil {
		return search.Result{}, err
	}
	page, size := pageWithin(query)
	response, err := i.client.Index(i.uid(owner)).SearchWithContext(ctx, query.Text, &meili.SearchRequest{
		Filter: filter(owner, query),
		// page and hitsPerPage, unlike offset and limit, count every match
		// instead of estimating.
		Page:                 page,
		HitsPerPage:          &size,
		AttributesToRetrieve: []string{fieldID},
	})
	if err != nil {
		err = describe(ctx, "search", err)
		if errors.Is(err, errIndexMissing) {
			return search.Result{IDs: []uuid.UUID{}}, nil
		}
		return search.Result{}, err
	}

	var hits []hit
	if err := response.Hits.DecodeInto(&hits); err != nil {
		return search.Result{}, fmt.Errorf("read search hits: %w", err)
	}
	ids := make([]uuid.UUID, len(hits))
	for n, h := range hits {
		if ids[n], err = uuid.Parse(h.ID); err != nil {
			return search.Result{}, fmt.Errorf("read search hit %d: %w", n, err)
		}
	}
	// NOTE: totalHits counts past pagination.maxTotalHits, which only caps
	// the hits a search can page through.
	total := min(response.TotalHits, search.MaxTotal)
	return search.Result{IDs: ids, Total: int(total)}, nil
}

// pageWithin returns query's page and page size as Meilisearch takes them,
// with the same hits: a page holds at most [search.MaxTotal], and a page
// past the last one with hits becomes the first such page.
//
// NOTE: Meilisearch computes (page - 1) * hitsPerPage without checking for
// overflow, so a far page, e.g. 2^62 + 1 of 4, wraps around to the first
// page on v1.56.1.
func pageWithin(query search.Query) (page, size int64) {
	size = int64(min(query.PageSize, search.MaxTotal))
	// The first page whose offset is at least MaxTotal, so it has no hits.
	beyond := (search.MaxTotal-1)/size + 2
	return min(int64(query.Page), beyond), size
}

// filter returns a search's filter, a list whose every expression must
// hold: always the owner first, then the accounts and mailboxes query
// names.
func filter(owner int64, query search.Query) []string {
	// The owner too, although the index is already the member's own: a
	// second guard, should a document ever reach the wrong index.
	expressions := []string{fieldOwner + " = " + quote(decimal(owner))}
	if len(query.Accounts) > 0 {
		ids := make([]string, len(query.Accounts))
		for n, id := range query.Accounts {
			ids[n] = quote(decimal(id))
		}
		expressions = append(expressions, fieldAccount+" IN ["+strings.Join(ids, ", ")+"]")
	}
	if len(query.ExcludeMailboxes) > 0 {
		ids := make([]string, len(query.ExcludeMailboxes))
		for n, id := range query.ExcludeMailboxes {
			ids[n] = quote(id.String())
		}
		// On a list field, IN holds when any of its values is listed.
		expressions = append(expressions, fieldMailboxes+" NOT IN ["+strings.Join(ids, ", ")+"]")
	}
	return expressions
}

// quote returns value as a double-quoted string in a filter.
//
// NOTE: Meilisearch's filter docs do not say how to escape. On v1.56.1 a
// backslash escapes a double quote or a backslash.
func quote(value string) string {
	// 1. Double each backslash, before step 2 adds backslashes of its own.
	value = strings.ReplaceAll(value, `\`, `\\`)
	// 2. Escape each double quote.
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}
