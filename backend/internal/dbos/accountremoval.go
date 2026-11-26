package dbos

import (
	"context"
	"errors"
	"log/slog"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"inbok/internal/connectedaccount"
)

// accountRemovalWorkflow names the account removal in DBOS's tables, where
// enqueued runs outlive the code that made them. Its runs use
// [googleQueue].
const accountRemovalWorkflow = "inbok.account-removal"

// AccountRemovalInput is the account removal workflow's input: the
// removal a disconnect saved. The step reads the token when it runs, so
// the input holds no secret.
type AccountRemovalInput struct {
	RemovalID int64
}

// accountRemovals revokes disconnected accounts' refresh tokens, e.g.
// [connectedaccount.Removals].
type accountRemovals interface {
	Revoke(ctx context.Context, removalID int64) (connectedaccount.RevokeOutcome, error)
	Discard(ctx context.Context, removalID int64) error
}

// revokeRetries spans about half an hour, like [noticeRetries]: enough for
// a short outage at Google, and no one waits for the removal.
var revokeRetries = noticeRetries

// registerAccountRemoval registers the workflow that revokes tokens with
// removals, or nothing when removals is nil, as when no Google client
// is configured.
func (w *Worker) registerAccountRemoval(removals accountRemovals) {
	if removals == nil {
		return
	}
	run := func(ctx dbos.Context, input AccountRemovalInput) (connectedaccount.RevokeOutcome, error) {
		return runAccountRemoval(ctx, removals, input, w.logger)
	}
	dbos.RegisterWorkflow(w.ctx, run, dbos.WithWorkflowName(accountRemovalWorkflow))
}

// runAccountRemoval revokes input's token, logs the outcome to logger by
// removal ID, and returns it. When Google keeps failing past the retries,
// it discards the removal, so the sealed token is not kept, and returns
// the removal's error.
func runAccountRemoval(ctx dbos.Context, removals accountRemovals, input AccountRemovalInput, logger *slog.Logger) (connectedaccount.RevokeOutcome, error) {
	// 1. Revoke. Revoking is repeat-safe: Google takes a token it no
	// longer knows as revoked, and a finished one leaves nothing to do.
	outcome, err := dbos.RunAsStep(ctx, func(stepCtx context.Context) (connectedaccount.RevokeOutcome, error) {
		return removals.Revoke(stepCtx, input.RemovalID)
	}, append(revokeRetries.options(), dbos.WithStepName("revoke token"))...)
	if ctx.Err() != nil {
		// The worker is stopping; the next one resumes at this step.
		return "", ctx.Err()
	}
	if err == nil {
		level := slog.LevelInfo
		if outcome == connectedaccount.RevokeUnreadable {
			level = slog.LevelWarn
		}
		logger.Log(ctx, level, "account removal", "removal_id", input.RemovalID, "outcome", outcome)
		return outcome, nil
	}

	// 2. Give up: revoking is best effort, and the token lapses unused.
	logger.WarnContext(ctx, "account removal failed", "removal_id", input.RemovalID, "err", err)
	_, discardErr := dbos.RunAsStep(ctx, func(stepCtx context.Context) (struct{}, error) {
		return struct{}{}, removals.Discard(stepCtx, input.RemovalID)
	}, append(revokeRetries.options(), dbos.WithStepName("discard token"))...)
	return "", errors.Join(err, discardErr)
}
