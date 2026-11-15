package meilisearch

import (
	"context"
	"strings"

	meili "github.com/meilisearch/meilisearch-go"

	"inbok/internal/search"
)

// indexSettings returns the settings every member's index gets. An update
// leaves the settings it omits as the index has them, which on a new index
// are Meilisearch's defaults, e.g. typo tolerance and ranking.
func indexSettings() meili.Settings {
	return meili.Settings{
		// In ADR005's priority: Meilisearch ranks a match in an earlier
		// field higher.
		SearchableAttributes: []string{fieldSubject, fieldFrom, fieldTo, fieldCC, fieldBodyText, fieldAltText},
		// Only the fields today's searches filter on, and the conversation,
		// which a search keeps one match of: Meilisearch takes only a
		// filterable field for that. A filter added later adds its field
		// here, and Meilisearch reindexes from the documents it holds.
		FilterableAttributes: []string{fieldOwner, fieldAccount, fieldMailboxes, fieldConversation},
		Pagination:           &meili.Pagination{MaxTotalHits: search.MaxTotal},
		// NOTE: a list of ranking rules replaces Meilisearch's whole list,
		// so it repeats v1.56.1's default rules, in their order. The date,
		// last, only orders matches the others leave equal, newest first:
		// otherwise they come in the order they were written, which a
		// rebuild shuffles.
		RankingRules: []string{"words", "typo", "proximity", "attributeRank", "sort", "wordPosition", "exactness", fieldDate + ":desc"},
		// NOTE: the client sends every typo setting, as none is omitempty,
		// so this repeats Meilisearch's defaults but for numbers: a number
		// a typo away, e.g. order 4471828 for 4471829, is another number,
		// not a misspelling.
		TypoTolerance: &meili.TypoTolerance{
			Enabled:             true,
			MinWordSizeForTypos: meili.MinWordSizeForTypos{OneTypo: oneTypoWordSize, TwoTypos: twoTyposWordSize},
			DisableOnWords:      []string{},
			DisableOnAttributes: []string{},
			DisableOnNumbers:    true,
		},
	}
}

// Meilisearch's default shortest words that may hold one typo and two.
const (
	oneTypoWordSize  = 5
	twoTyposWordSize = 9
)

// Prepare applies the settings to every member's index and waits until
// Meilisearch has applied them. An index a member gets later receives them
// from [Index.Upsert].
//
// DESIGN: it sends every setting each time rather than reading them first
// to compare. Meilisearch runs an update that changes nothing in a few
// milliseconds without reindexing, and queues two for one index, from two
// processes, one after the other, so either way they end the same.
func (i *Index) Prepare(ctx context.Context) error {
	uids, err := i.members(ctx)
	if err != nil {
		return err
	}

	// 1. Send every update before waiting for any, so Meilisearch applies
	// them while the waits run.
	tasks := make([]*meili.TaskInfo, len(uids))
	for n, uid := range uids {
		if tasks[n], err = i.apply(ctx, uid); err != nil {
			return describe(ctx, "apply search settings", err)
		}
	}

	// 2. Wait for each.
	for _, task := range tasks {
		if err := i.wait(ctx, "apply search settings", task, nil); err != nil {
			return err
		}
	}
	return nil
}

// members returns the names of every member's index, page by page of
// Meilisearch's list of indexes.
func (i *Index) members(ctx context.Context) ([]string, error) {
	var uids []string
	for offset := int64(0); ; offset += i.listLimit {
		page, err := i.client.ListIndexesWithContext(ctx, &meili.IndexesQuery{Limit: i.listLimit, Offset: offset})
		if err != nil {
			return nil, describe(ctx, "list search indexes", err)
		}
		for _, index := range page.Results {
			if strings.HasPrefix(index.UID, i.prefix) {
				uids = append(uids, index.UID)
			}
		}
		if offset+i.listLimit >= page.Total {
			return uids, nil
		}
	}
}

// apply sends the settings to the index uid, which Meilisearch creates if
// it is missing. It returns the task without waiting for it.
func (i *Index) apply(ctx context.Context, uid string) (*meili.TaskInfo, error) {
	settings := i.settings
	return i.client.Index(uid).UpdateSettingsWithContext(ctx, &settings)
}
