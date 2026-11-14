package dbos

import (
	"context"
	"log/slog"
	"strconv"
	"time"
	"uuid"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"inbok/internal/search"
)

// The search index's workflows' names in DBOS's tables, where enqueued
// runs outlive the code that made them. Their runs use [searchQueue].
const (
	searchIndexingWorkflow       = "inbok.search-indexing"
	searchAccountRemovalWorkflow = "inbok.search-account-removal"
)

// SearchIndexingInput is the search indexing workflow's input: the
// messages a transaction changed, by ID, and their member. The steps read
// the messages when they run, so the input names no mail.
type SearchIndexingInput struct {
	OwnerUserID int64
	MessageIDs  []uuid.UUID
}

// SearchAccountRemovalInput is the search account removal workflow's
// input: a member's disconnected account.
type SearchAccountRemovalInput struct {
	OwnerUserID int64
	AccountID   int64
}

// searchIndex keeps members' search indexes in step with their mail,
// e.g. [search.Indexer].
type searchIndex interface {
	Index(ctx context.Context, owner int64, ids []uuid.UUID) error
	RemoveAccount(ctx context.Context, owner, accountID int64) error
}

// searchRetries spans about an hour, 1, 2, 4, 8, then 15 minutes three
// times: enough for Meilisearch's restart or a short outage, while the
// member's later runs wait behind the retrying one (ADR005). As with every
// policy, a worker restarted meanwhile starts the count again.
var searchRetries = retries{attempts: 7, base: time.Minute, ceiling: 15 * time.Minute}

// searchPartition returns the partition of [searchQueue] that owner's runs
// share, so they run one at a time.
func searchPartition(owner int64) string {
	return strconv.FormatInt(owner, 10)
}

// registerSearch registers the workflows that write to index, or nothing
// when index is nil.
func (w *Worker) registerSearch(index searchIndex) {
	if index == nil {
		return
	}
	indexing := func(ctx dbos.Context, input SearchIndexingInput) (int, error) {
		return runSearchIndexing(ctx, index, input, w.logger)
	}
	dbos.RegisterWorkflow(w.ctx, indexing, dbos.WithWorkflowName(searchIndexingWorkflow))
	removal := func(ctx dbos.Context, input SearchAccountRemovalInput) (struct{}, error) {
		return struct{}{}, runSearchAccountRemoval(ctx, index, input, w.logger)
	}
	dbos.RegisterWorkflow(w.ctx, removal, dbos.WithWorkflowName(searchAccountRemovalWorkflow))
}

// runSearchIndexing brings input's messages up to date in the index, one
// step per batch of [search.BatchSize], and returns how many it took.
// When the index keeps failing past the retries, it logs to logger and
// returns the index's error; the messages are then out of date in the
// index until they change again or the index is rebuilt.
func runSearchIndexing(ctx dbos.Context, index searchIndex, input SearchIndexingInput, logger *slog.Logger) (int, error) {
	ids := input.MessageIDs
	for len(ids) > 0 {
		batch := ids[:min(len(ids), search.BatchSize)]
		ids = ids[len(batch):]
		// Indexing is repeat-safe: it writes what PostgreSQL holds when it
		// runs, so a repeat writes the same or newer.
		_, err := dbos.RunAsStep(ctx, func(stepCtx context.Context) (struct{}, error) {
			return struct{}{}, index.Index(stepCtx, input.OwnerUserID, batch)
		}, append(searchRetries.options(), dbos.WithStepName("index messages"))...)
		if err != nil {
			if ctx.Err() == nil {
				logger.WarnContext(ctx, "search indexing failed", "user_id", input.OwnerUserID, "err", err)
			}
			return 0, err
		}
	}
	return len(input.MessageIDs), nil
}

// runSearchAccountRemoval removes input's account from the index. When
// the index keeps failing past the retries, it logs to logger and returns
// the index's error; the account's messages then stay found until the
// index is rebuilt, though they no longer open.
func runSearchAccountRemoval(ctx dbos.Context, index searchIndex, input SearchAccountRemovalInput, logger *slog.Logger) error {
	// Removing is repeat-safe: an account with no documents left removes
	// nothing.
	_, err := dbos.RunAsStep(ctx, func(stepCtx context.Context) (struct{}, error) {
		return struct{}{}, index.RemoveAccount(stepCtx, input.OwnerUserID, input.AccountID)
	}, append(searchRetries.options(), dbos.WithStepName("remove account"))...)
	if err != nil && ctx.Err() == nil {
		logger.WarnContext(ctx, "search account removal failed", "user_id", input.OwnerUserID, "account_id", input.AccountID, "err", err)
	}
	return err
}
