package dbos

import (
	"context"
	"fmt"
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

// searchRetries spans about an hour, 1, 2, 4, 8, then 15 minutes three times:
// enough for Meilisearch's restart or a short outage, while the member's
// later runs wait behind the retrying one. As with every policy, a worker
// restarted meanwhile starts the count again.
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

// The search rebuild's names in DBOS's tables. A rebuild of every member
// runs on [searchRebuildQueue] when inbok search rebuild asks for it, and
// nightly on its schedule; each member's rebuild runs on [searchQueue], in
// the member's partition.
const (
	searchRebuildWorkflow         = "inbok.search-rebuild"
	searchRebuildScheduleWorkflow = "inbok.search-rebuild-schedule"
	searchRebuildSchedule         = "inbok.search-rebuild.nightly"
	searchMemberRebuildWorkflow   = "inbok.search-member-rebuild"

	// searchRebuildCron fires at 03:00 UTC every day. DBOS's cron has a
	// seconds field first.
	searchRebuildCron = "0 0 3 * * *"
)

// searchMemberRebuildInput is a member's search rebuild workflow's input:
// the member whose index it rebuilds.
type searchMemberRebuildInput struct {
	OwnerUserID int64
}

// SearchRebuildOutput is what a rebuild of every member did, summed over
// the members whose rebuild succeeded, as [search.Rebuilt] counts them.
type SearchRebuildOutput struct {
	Members   int
	Messages  int
	Leftovers int
	Removed   int
}

// searchRebuild rebuilds members' search indexes from what storage holds,
// e.g. [search.Rebuild].
type searchRebuild interface {
	Owners(ctx context.Context) ([]int64, error)
	Owner(ctx context.Context, owner int64) (search.Rebuilt, error)
}

// registerSearchRebuild registers the workflows that rebuild members'
// search indexes, and schedules the rebuild of every member nightly; with
// rebuild nil it deletes the schedule instead, which an earlier start may
// have saved. It returns DBOS's error saving or deleting the schedule.
//
// DESIGN: The nightly run tries to repair what live indexing cannot see
// went wrong, e.g. its runs that gave up while Meilisearch was down. DBOS
// does not make up a night the worker was down, and a run that fails or
// waits leaves the repair to a later one. It rewrites every message each night, which a demo's mail
// makes cheap; at a larger scale, compare first, e.g. counts, and rebuild
// only the members that differ.
func (w *Worker) registerSearchRebuild(rebuild searchRebuild) error {
	if rebuild == nil {
		return dbos.DeleteSchedule(w.ctx, searchRebuildSchedule)
	}
	member := func(ctx dbos.Context, input searchMemberRebuildInput) (search.Rebuilt, error) {
		return runSearchMemberRebuild(ctx, rebuild, input, w.logger)
	}
	dbos.RegisterWorkflow(w.ctx, member, dbos.WithWorkflowName(searchMemberRebuildWorkflow))
	all := func(ctx dbos.Context, _ struct{}) (SearchRebuildOutput, error) {
		return runSearchRebuild(ctx, rebuild, w.logger)
	}
	dbos.RegisterWorkflow(w.ctx, all, dbos.WithWorkflowName(searchRebuildWorkflow))
	scheduled := func(ctx dbos.Context, _ dbos.ScheduledWorkflowInput) (SearchRebuildOutput, error) {
		return runSearchRebuild(ctx, rebuild, w.logger)
	}
	dbos.RegisterWorkflow(w.ctx, scheduled, dbos.WithWorkflowName(searchRebuildScheduleWorkflow))

	return dbos.ApplySchedules(w.ctx, []dbos.ScheduleSpec{{
		ScheduleName: searchRebuildSchedule,
		Schedule:     searchRebuildCron,
		CronTimezone: "UTC",
		WorkflowName: searchRebuildScheduleWorkflow,
	}})
}

// runSearchRebuild rebuilds every member's index: it lists the owners
// rebuild covers, enqueues each one's rebuild in their partition of
// [searchQueue], and waits for all of them. It returns what they did, and
// an error naming the members whose rebuild failed, once every one ended.
//
// DESIGN: It runs outside every member's partition, so it can wait for
// the members' runs, which wait their turn behind the members' live
// indexing.
func runSearchRebuild(ctx dbos.Context, rebuild searchRebuild, logger *slog.Logger) (SearchRebuildOutput, error) {
	// 1. List the owners once: a resumed run keeps what it listed.
	owners, err := dbos.RunAsStep(ctx, func(stepCtx context.Context) ([]int64, error) {
		return rebuild.Owners(stepCtx)
	}, append(searchRetries.options(), dbos.WithStepName("list owners"))...)
	if err != nil {
		return SearchRebuildOutput{}, err
	}

	// 2. Enqueue each, which DBOS records as a step of this run, so a
	// resumed run enqueues none twice.
	handles := make([]dbos.WorkflowHandle[search.Rebuilt], len(owners))
	for n, owner := range owners {
		handles[n], err = dbos.Enqueue[search.Rebuilt](ctx, searchQueue, searchMemberRebuildWorkflow, searchMemberRebuildInput{OwnerUserID: owner},
			dbos.WithEnqueueQueuePartitionKey(searchPartition(owner)),
		)
		if err != nil {
			return SearchRebuildOutput{}, err
		}
	}

	// 3. Wait for each, counting the failed rather than stopping at the
	// first, so every member gets rebuilt.
	var output SearchRebuildOutput
	var failed []int64
	for n, handle := range handles {
		rebuilt, err := handle.GetResult()
		if ctx.Err() != nil {
			// Stopping: the run stays pending and resumes its wait.
			return SearchRebuildOutput{}, ctx.Err()
		}
		if err != nil {
			failed = append(failed, owners[n])
			continue
		}
		output.Members++
		output.Messages += rebuilt.Messages
		output.Leftovers += rebuilt.Leftovers
		if rebuilt.Removed {
			output.Removed++
		}
	}
	if len(failed) > 0 {
		logger.WarnContext(ctx, "search rebuild failed", "user_ids", failed)
		return output, fmt.Errorf("search rebuild failed for %d of %d members, user IDs %v", len(failed), len(owners), failed)
	}
	return output, nil
}

// runSearchMemberRebuild rebuilds input's member's index, and returns what
// it did. When the rebuild keeps failing past the retries, it logs to
// logger and returns the error; the member's index then stays as it was,
// partly rebuilt, until another rebuild.
func runSearchMemberRebuild(ctx dbos.Context, rebuild searchRebuild, input searchMemberRebuildInput, logger *slog.Logger) (search.Rebuilt, error) {
	// One step for the whole member, not one per batch: a step DBOS
	// recorded is skipped when the run resumes, though the index it wrote
	// to may since have been replaced, e.g. by a new, empty Meilisearch.
	// An interrupted step was not recorded, so a resumed run repeats the
	// member whole, which a rebuild allows. A finished step is reused even
	// so: an index replaced after it needs a new rebuild, which upgrade.sh
	// and the restore steps run.
	rebuilt, err := dbos.RunAsStep(ctx, func(stepCtx context.Context) (search.Rebuilt, error) {
		return rebuild.Owner(stepCtx, input.OwnerUserID)
	}, append(searchRetries.options(), dbos.WithStepName("rebuild member"))...)
	if err != nil && ctx.Err() == nil {
		logger.WarnContext(ctx, "search member rebuild failed", "user_id", input.OwnerUserID, "err", err)
	}
	return rebuilt, err
}
