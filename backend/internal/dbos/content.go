package dbos

import (
	"context"
	"log/slog"
	"uuid"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"amplop/internal/messagecontent"
)

// The content deletions' names in DBOS's tables, where enqueued runs
// outlive the code that made them. Their runs use [contentQueue].
const (
	contentDeletionWorkflow        = "amplop.content-deletion"
	accountContentDeletionWorkflow = "amplop.account-content-deletion"
)

// ContentDeletionInput is the content deletion workflow's input: the
// blobs of messages a transaction deleted, by ID, which names no mail.
type ContentDeletionInput struct {
	AccountID int64
	BlobIDs   []uuid.UUID
}

// AccountContentDeletionInput is the account content deletion workflow's
// input: the disconnected account whose content goes.
type AccountContentDeletionInput struct {
	AccountID int64
}

// contentStore deletes stored message content, e.g. s3.Store.
type contentStore interface {
	Delete(ctx context.Context, refs []messagecontent.Ref) error
	DeleteAccount(ctx context.Context, account int64) error
}

// contentRetries spans about half an hour, like [noticeRetries]: enough
// for the store's restart, and the sweep deletes what is left.
var contentRetries = noticeRetries

// registerContentDeletion registers the workflow that deletes content
// from store, or nothing when store is nil.
func (w *Worker) registerContentDeletion(store contentStore) {
	if store == nil {
		return
	}
	run := func(ctx dbos.Context, input ContentDeletionInput) (int, error) {
		return runContentDeletion(ctx, store, input, w.logger)
	}
	dbos.RegisterWorkflow(w.ctx, run, dbos.WithWorkflowName(contentDeletionWorkflow))
}

// runContentDeletion deletes input's content from store and returns how
// many it deleted. When the store keeps failing past the retries, it logs
// to logger and returns the store's error.
//
// DESIGN: Content no part names is also what the scheduled cleanup's sweep
// deletes a day later, so a run that gives up leaves the content only that
// long.
func runContentDeletion(ctx dbos.Context, store contentStore, input ContentDeletionInput, logger *slog.Logger) (int, error) {
	refs := make([]messagecontent.Ref, len(input.BlobIDs))
	for i, id := range input.BlobIDs {
		refs[i] = messagecontent.Ref{Account: input.AccountID, ID: id}
	}
	// Deleting is repeat-safe: content already gone is no error.
	_, err := dbos.RunAsStep(ctx, func(stepCtx context.Context) (struct{}, error) {
		return struct{}{}, store.Delete(stepCtx, refs)
	}, append(contentRetries.options(), dbos.WithStepName("delete content"))...)
	if err != nil {
		if ctx.Err() == nil {
			logger.WarnContext(ctx, "content deletion failed", "account_id", input.AccountID, "err", err)
		}
		return 0, err
	}
	return len(refs), nil
}

// registerAccountContentDeletion registers the workflow that deletes a
// disconnected account's content from store, or nothing when store is nil.
//
// DESIGN: A job of its own, not a step of the account removal, so a store
// that fails or stalls never holds the token's revocation, and a worker
// without Google settings still deletes the content.
func (w *Worker) registerAccountContentDeletion(store contentStore) {
	if store == nil {
		return
	}
	run := func(ctx dbos.Context, input AccountContentDeletionInput) (struct{}, error) {
		return struct{}{}, runAccountContentDeletion(ctx, store, input, w.logger)
	}
	dbos.RegisterWorkflow(w.ctx, run, dbos.WithWorkflowName(accountContentDeletionWorkflow))
}

// runAccountContentDeletion deletes all of input's account's content from
// store. When the store keeps failing past the retries, it logs to logger
// and returns the store's error; the sweep deletes the content a day
// later, as no part names it any more.
func runAccountContentDeletion(ctx dbos.Context, store contentStore, input AccountContentDeletionInput, logger *slog.Logger) error {
	// Deleting is repeat-safe: an empty prefix deletes nothing.
	_, err := dbos.RunAsStep(ctx, func(stepCtx context.Context) (struct{}, error) {
		return struct{}{}, store.DeleteAccount(stepCtx, input.AccountID)
	}, append(contentRetries.options(), dbos.WithStepName("delete account content"))...)
	if err != nil && ctx.Err() == nil {
		logger.WarnContext(ctx, "account content deletion failed", "account_id", input.AccountID, "err", err)
	}
	return err
}
