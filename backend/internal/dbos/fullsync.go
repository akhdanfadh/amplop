package dbos

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"inbok/internal/mailsync"
)

// fullSyncWorkflow names the full sync in DBOS's tables, where enqueued
// runs outlive the code that made them. Its runs use [googleQueue].
const fullSyncWorkflow = "inbok.full-sync"

// FullSyncInput is the full sync workflow's input: the account whose mail
// it copies.
//
// DESIGN: Each step takes its own access token, so the input holds no
// secret (ADR003).
type FullSyncInput struct {
	AccountID int64
}

// fullSyncs runs the steps of full syncs, e.g. [mailsync.FullSync].
type fullSyncs interface {
	Start(ctx context.Context, accountID int64) (mailsync.Run, error)
	CopyPage(ctx context.Context, accountID int64, run mailsync.Run, page int, pageToken string) (string, error)
	DeleteEarlier(ctx context.Context, accountID int64, run mailsync.Run) (bool, error)
	Finish(ctx context.Context, accountID int64, run mailsync.Run) error
	Wait(ctx context.Context, accountID int64, run mailsync.Run) error
	Fail(ctx context.Context, accountID int64, run mailsync.Run, failure mailsync.Failure) error
}

// syncRetries spans about five minutes, 30 seconds, then 1, 2, and 2 minutes,
// retried within each step: each Gmail call has already retried its own rate
// limits for about 15 seconds, so these cover a short outage, at Google or of
// inbok's database, before the full sync fails and waits for its next start.
var syncRetries = retries{attempts: 4, base: 30 * time.Second, ceiling: 2 * time.Minute}

// fullSyncDeduplication returns the deduplication ID of accountID's full
// syncs on [googleQueue]: DBOS refuses a second while one is queued or
// running, so an account has one full sync at a time.
func fullSyncDeduplication(accountID int64) string {
	return "full-sync-" + strconv.FormatInt(accountID, 10)
}

// registerFullSync registers the full sync workflow with syncs, or nothing
// when syncs is nil, as when no Google client is configured.
func (w *Worker) registerFullSync(syncs fullSyncs) {
	if syncs == nil {
		return
	}
	run := func(ctx dbos.Context, input FullSyncInput) (mailsync.Status, error) {
		return runFullSync(ctx, syncs, input, w.logger)
	}
	dbos.RegisterWorkflow(w.ctx, run, dbos.WithWorkflowName(fullSyncWorkflow))
}

// errFullSyncFailed means a full sync ended failed, so DBOS records the
// run as failed too.
var errFullSyncFailed = errors.New("full sync failed")

// stepResult is a full sync step's output: its value, or how an error
// ended the full sync.
//
// DESIGN: An error that ends the full sync, retried or not, is returned as
// a value, which DBOS records whole. DBOS records a step's error as text,
// which [errors.Is] no longer matches when a resumed run reads it back, so
// the failure's kind would be lost.
type stepResult[T any] struct {
	Value   T
	Ending  mailsync.Ending
	Failure mailsync.Failure
}

// syncStep runs fn as the step name, retrying it within the step with
// syncRetries while [mailsync.EndingOf] says retrying may help. It returns
// fn's value, or how its error ended the full sync: past the retries, with
// [mailsync.EndingFail] and [mailsync.FailureOf], after logging the error to
// logger. It returns an error only when the worker is stopping, and the run
// must return at once.
func syncStep[T any](ctx dbos.Context, logger *slog.Logger, name string, fn func(context.Context) (T, error)) (stepResult[T], error) {
	return dbos.RunAsStep(ctx, func(stepCtx context.Context) (stepResult[T], error) {
		return retrySync(stepCtx, logger, name, fn)
	}, dbos.WithStepName(name))
}

// retrySync calls fn until it succeeds, its error ends the full sync, or
// syncRetries runs out, waiting between tries as syncRetries says. It
// returns the result as [syncStep] describes, or ctx's error once ctx ends.
//
// NOTE: The waits are in memory, as DBOS's own step retries are: a worker
// stopped during one runs the step again with a fresh count.
func retrySync[T any](ctx context.Context, logger *slog.Logger, name string, fn func(context.Context) (T, error)) (stepResult[T], error) {
	for try := 0; ; try++ {
		// 1. Try the step.
		value, err := fn(ctx)
		if err == nil {
			return stepResult[T]{Value: value}, nil
		}

		// 2. Check for a stopping worker before sorting the error: its
		// cancelled call would otherwise be recorded as a failure. The next
		// worker runs the step again.
		if ctx.Err() != nil {
			return stepResult[T]{}, ctx.Err()
		}

		// 3. End the sync now on an error retrying cannot mend.
		ending := mailsync.EndingOf(err)
		if ending != mailsync.EndingRetry {
			return stepResult[T]{Ending: ending, Failure: mailsync.FailureOf(err)}, nil
		}

		// 4. Past the retries, fail it, logging the error, which the
		// recorded value leaves out.
		if try == syncRetries.attempts {
			logger.WarnContext(ctx, "full sync step failed", "step", name, "err", err)
			return stepResult[T]{Ending: mailsync.EndingFail, Failure: mailsync.FailureOf(err)}, nil
		}

		// 5. Wait before the next try, unless the worker stops meanwhile.
		if err := pause(ctx, syncRetries.wait(try)); err != nil {
			return stepResult[T]{}, err
		}
	}
}

// pause waits for d, or returns ctx's error once ctx ends first.
func pause(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// runFullSync runs account input.AccountID's full sync and returns the
// status it left: complete, waiting, failed, or empty when the account went
// or a newer full sync took over. It logs to logger how a full sync that
// did not complete ended, by account ID. It returns an error wrapping
// errFullSyncFailed when it failed, an error recording the end, or the
// context's error when the worker is stopping.
func runFullSync(ctx dbos.Context, syncs fullSyncs, input FullSyncInput, logger *slog.Logger) (mailsync.Status, error) {
	accountID := input.AccountID
	logger = logger.With("account_id", accountID)

	// 1. Start a generation, with the position read before listing.
	started, err := syncStep(ctx, logger, "start", func(stepCtx context.Context) (mailsync.Run, error) {
		return syncs.Start(stepCtx, accountID)
	})
	if err != nil {
		return "", err
	}
	if started.Ending != 0 {
		return endFullSync(ctx, syncs, accountID, mailsync.Run{}, started.Ending, started.Failure, logger)
	}
	run := started.Value

	// 2. Copy the window page by page, newest first; each page's token
	// comes from the step before.
	pageToken := ""
	for page := 0; ; page++ {
		copied, err := syncStep(ctx, logger, "copy page", func(stepCtx context.Context) (string, error) {
			return syncs.CopyPage(stepCtx, accountID, run, page, pageToken)
		})
		if err != nil {
			return "", err
		}
		if copied.Ending != 0 {
			return endFullSync(ctx, syncs, accountID, run, copied.Ending, copied.Failure, logger)
		}
		if copied.Value == "" {
			break
		}
		pageToken = copied.Value
	}

	// 3. Delete what the provider no longer lists, only once every page is
	// saved: a message is earlier until this generation saves it.
	for {
		deleted, err := syncStep(ctx, logger, "delete earlier", func(stepCtx context.Context) (bool, error) {
			return syncs.DeleteEarlier(stepCtx, accountID, run)
		})
		if err != nil {
			return "", err
		}
		if deleted.Ending != 0 {
			return endFullSync(ctx, syncs, accountID, run, deleted.Ending, deleted.Failure, logger)
		}
		if !deleted.Value {
			break
		}
	}

	// 4. Record the full sync as complete.
	finished, err := syncStep(ctx, logger, "finish", func(stepCtx context.Context) (struct{}, error) {
		return struct{}{}, syncs.Finish(stepCtx, accountID, run)
	})
	if err != nil {
		return "", err
	}
	if finished.Ending != 0 {
		return endFullSync(ctx, syncs, accountID, run, finished.Ending, finished.Failure, logger)
	}
	return mailsync.StatusComplete, nil
}

// endFullSync records how account accountID's full sync run ended: back to
// waiting for [mailsync.EndingWait], failed with failure for
// [mailsync.EndingFail], and nothing for [mailsync.EndingGone]. It logs the
// end to logger, and returns the status it left and, for a failure, an
// error wrapping errFullSyncFailed, joined with any error recording it.
func endFullSync(ctx dbos.Context, syncs fullSyncs, accountID int64, run mailsync.Run, ending mailsync.Ending, failure mailsync.Failure, logger *slog.Logger) (mailsync.Status, error) {
	// 1. Record nothing while the worker is stopping: the next one resumes
	// at the step.
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	// 2. Record how the run ended.
	switch ending {
	case mailsync.EndingGone:
		logger.InfoContext(ctx, "full sync ended", "outcome", "gone")
		return "", nil
	case mailsync.EndingWait:
		logger.InfoContext(ctx, "full sync ended", "outcome", mailsync.StatusWaiting)
		_, err := dbos.RunAsStep(ctx, func(stepCtx context.Context) (struct{}, error) {
			return struct{}{}, syncs.Wait(stepCtx, accountID, run)
		}, append(syncRetries.options(), dbos.WithStepName("wait"))...)
		return mailsync.StatusWaiting, err
	default:
		logger.WarnContext(ctx, "full sync failed", "failure", failure)
		_, err := dbos.RunAsStep(ctx, func(stepCtx context.Context) (struct{}, error) {
			return struct{}{}, syncs.Fail(stepCtx, accountID, run, failure)
		}, append(syncRetries.options(), dbos.WithStepName("fail"))...)
		return mailsync.StatusFailed, errors.Join(fmt.Errorf("%w: %s", errFullSyncFailed, failure), err)
	}
}
