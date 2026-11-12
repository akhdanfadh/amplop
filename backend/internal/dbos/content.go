package dbos

import (
	"context"
	"log/slog"
	"uuid"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"inbok/internal/messagecontent"
)

// contentDeletionWorkflow names the content deletion in DBOS's tables,
// where enqueued runs outlive the code that made them. Its runs use
// [contentQueue].
const contentDeletionWorkflow = "inbok.content-deletion"

// ContentDeletionInput is the content deletion workflow's input: the
// blobs of messages a transaction deleted, by ID, which names no mail.
type ContentDeletionInput struct {
	AccountID int64
	BlobIDs   []uuid.UUID
}

// contentStore deletes stored message content, e.g. s3.Store.
type contentStore interface {
	Delete(ctx context.Context, refs []messagecontent.Ref) error
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
// DESIGN: Content no part names is also what the scheduled cleanup's
// sweep deletes a day later (ADR009), so a run that gives up leaves the
// content only that long.
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
