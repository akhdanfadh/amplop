package meilisearch

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"uuid"

	meili "github.com/meilisearch/meilisearch-go"

	"amplop/internal/search"
)

// Owners returns the owner of each member's index, page by page of
// Meilisearch's list of indexes. It returns an error for an index named
// like a member's but not exactly as [Index] names one, rather than skip
// it.
func (i *Index) Owners(ctx context.Context) ([]int64, error) {
	uids, err := i.members(ctx)
	if err != nil {
		return nil, err
	}
	owners := make([]int64, 0, len(uids))
	for _, uid := range uids {
		owner, err := strconv.ParseInt(strings.TrimPrefix(uid, i.prefix), 10, 64)
		// Only the name amplop gives the owner's index, e.g. not
		// messages_042, which a rebuild of owner 42 would never delete.
		if err != nil || search.ValidateOwner(owner) != nil || uid != i.uid(owner) {
			return nil, fmt.Errorf("list search index owners: index %q names no user ID", uid)
		}
		owners = append(owners, owner)
	}
	return owners, nil
}

// DocumentIDs returns the message IDs of owner's documents, page by page
// of Meilisearch's documents, and none when owner has no index. It returns
// [search.ErrNoOwner] before reading, and an error for a document whose ID
// is not a message ID, rather than skip it.
//
// DESIGN: it pages by offset, which skips or repeats documents written or
// deleted between two pages; its caller reads while no one writes owner's
// index, as the search queue runs one of owner's runs at a time.
func (i *Index) DocumentIDs(ctx context.Context, owner int64) ([]uuid.UUID, error) {
	if err := search.ValidateOwner(owner); err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for offset := int64(0); ; offset += i.documentPage {
		var page meili.DocumentsResult
		query := &meili.DocumentsQuery{Offset: offset, Limit: i.documentPage, Fields: []string{fieldID}}
		if err := i.client.Index(i.uid(owner)).GetDocumentsWithContext(ctx, query, &page); err != nil {
			// An owner with no index has no documents.
			return nil, ignoreMissing(describe(ctx, "list search documents", err))
		}
		var rows []struct {
			ID string `json:"id"`
		}
		if err := page.Results.DecodeInto(&rows); err != nil {
			return nil, fmt.Errorf("list search documents: %w", err)
		}
		for _, row := range rows {
			id, err := uuid.Parse(row.ID)
			if err != nil {
				return nil, fmt.Errorf("list search documents: a document's ID is not a message ID")
			}
			ids = append(ids, id)
		}
		if offset+i.documentPage >= page.Total {
			return ids, nil
		}
	}
}

// DeleteOwner deletes owner's index and waits until Meilisearch has
// applied it. It returns [search.ErrNoOwner] before deleting.
func (i *Index) DeleteOwner(ctx context.Context, owner int64) error {
	if err := search.ValidateOwner(owner); err != nil {
		return err
	}
	task, err := i.client.DeleteIndexWithContext(ctx, i.uid(owner))
	return ignoreMissing(i.wait(ctx, "delete a search index", task, err))
}
