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

const (
	// syncWorkflow, syncScheduleWorkflow, and syncSchedule name
	// synchronization in DBOS's tables, where enqueued runs and the schedule
	// outlive the code that made them. Sync runs use [googleQueue].
	syncWorkflow         = "inbok.sync"
	syncScheduleWorkflow = "inbok.sync-schedule"
	syncSchedule         = "inbok.sync.minutely"

	// syncCron fires at the start of every minute (ADR001, "Polling").
	// DBOS's cron has a seconds field first.
	syncCron = "0 * * * * *"
)

// SyncInput is the sync workflow's input: the account it synchronizes.
//
// DESIGN: Each step takes its own access token, so the input holds no
// secret (ADR003).
type SyncInput struct {
	AccountID int64
}

// SyncOutput is what a sync run did: the kind it ran, empty when it ended
// before choosing, and the full sync status it left, empty when the account
// went or a newer sync took over.
type SyncOutput struct {
	Kind   mailsync.Kind
	Status mailsync.Status
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

// polls chooses a sync run's kind and runs the steps of polls, e.g.
// [mailsync.Poll].
type polls interface {
	Plan(ctx context.Context, accountID int64) (mailsync.Plan, error)
	ReadChanges(ctx context.Context, accountID int64, position, pageToken string) (mailsync.ChangePage, error)
	ApplyChanges(ctx context.Context, accountID int64, from mailsync.PollFrom, pageToken string, page mailsync.ChangePage, part int) (bool, error)
	Fail(ctx context.Context, accountID int64, position string, failure mailsync.Failure) error
}

// dueSyncs picks the accounts whose sync run should start now, e.g.
// [mailsync.Schedule].
type dueSyncs interface {
	Due(ctx context.Context) ([]int64, error)
}

// pollEveryPages is how many full sync pages run between the polls inside
// the full sync's run, inbok's choice: at about 25 seconds a page, new mail
// arrives within about two minutes while a long copy goes on (ADR001,
// "Polling"). Counted in pages, so a resumed run polls where the first did.
const pollEveryPages = 4

// syncRetries spans about five minutes, 30 seconds, then 1, 2, and 2 minutes,
// retried within each step: each Gmail call has already retried its own rate
// limits for about 15 seconds, so these cover a short outage, at Google or of
// inbok's database, before the sync fails.
var syncRetries = retries{attempts: 4, base: 30 * time.Second, ceiling: 2 * time.Minute}

// syncDeduplication returns the deduplication ID of accountID's sync runs on
// [googleQueue]: DBOS refuses a second while one is queued or running, so an
// account has one sync, poll or full, at a time.
func syncDeduplication(accountID int64) string {
	return "sync-" + strconv.FormatInt(accountID, 10)
}

// registerSync registers the sync workflow with full and polls, or nothing
// when either is nil, as when no Google client is configured.
func (w *Worker) registerSync(full fullSyncs, polls polls) {
	if full == nil || polls == nil {
		return
	}
	run := func(ctx dbos.Context, input SyncInput) (SyncOutput, error) {
		return runSync(ctx, full, polls, input, w.logger)
	}
	dbos.RegisterWorkflow(w.ctx, run, dbos.WithWorkflowName(syncWorkflow))
}

// registerSyncSchedule registers the workflow that starts the sync runs due
// pick, and schedules it every minute; with due nil, as when no Google client
// is configured, it deletes the schedule instead, which an earlier start may
// have saved. It returns DBOS's error saving or deleting the schedule.
//
// DESIGN: The schedule waits for the next minute instead of making up for
// minutes the worker was down: one run finds every account that is due.
func (w *Worker) registerSyncSchedule(due dueSyncs) error {
	if due == nil {
		return dbos.DeleteSchedule(w.ctx, syncSchedule)
	}
	run := func(ctx dbos.Context, _ dbos.ScheduledWorkflowInput) (int, error) {
		return startDueSyncs(ctx, due)
	}
	dbos.RegisterWorkflow(w.ctx, run, dbos.WithWorkflowName(syncScheduleWorkflow))

	return dbos.ApplySchedules(w.ctx, []dbos.ScheduleSpec{{
		ScheduleName: syncSchedule,
		Schedule:     syncCron,
		CronTimezone: "UTC",
		WorkflowName: syncScheduleWorkflow,
	}})
}

// startDueSyncs enqueues a sync run for each account due picks, and returns
// how many it asked for. An account whose sync is already queued or running
// keeps that one. It returns the first error picking or enqueueing.
func startDueSyncs(ctx dbos.Context, due dueSyncs) (int, error) {
	// 1. Pick the accounts, which reads the database and asks the provider
	// for positions, once: a resumed run keeps what it picked.
	accounts, err := dbos.RunAsStep(ctx, func(stepCtx context.Context) ([]int64, error) {
		return due.Due(stepCtx)
	}, dbos.WithStepName("pick due syncs"))
	if err != nil {
		return 0, err
	}

	// 2. Enqueue each, which DBOS records as a step of this run.
	for _, accountID := range accounts {
		_, err := dbos.Enqueue[SyncOutput](ctx, googleQueue, syncWorkflow, SyncInput{AccountID: accountID},
			dbos.WithEnqueueDeduplicationID(syncDeduplication(accountID)),
			dbos.WithEnqueueDeduplicationPolicy(dbos.DeduplicationPolicyReturnExisting),
		)
		if err != nil {
			return 0, err
		}
	}
	return len(accounts), nil
}

// errSyncFailed means a sync ended failed, so DBOS records the run as
// failed too.
var errSyncFailed = errors.New("sync failed")

// stepResult is a sync step's output: its value, or how an error ended the
// sync.
//
// DESIGN: An error that ends the sync, retried or not, is returned as a
// value, which DBOS records whole. DBOS records a step's error as text,
// which [errors.Is] no longer matches when a resumed run reads it back, so
// the failure's kind would be lost.
type stepResult[T any] struct {
	Value   T
	Ending  mailsync.Ending
	Failure mailsync.Failure
}

// syncStep runs fn as the step name, retrying it within the step with
// syncRetries while [mailsync.EndingOf] says retrying may help. It returns
// fn's value, or how its error ended the sync: past the retries, with
// [mailsync.EndingFail] and [mailsync.FailureOf], after logging the error to
// logger. It returns an error only when the worker is stopping, and the run
// must return at once.
func syncStep[T any](ctx dbos.Context, logger *slog.Logger, name string, fn func(context.Context) (T, error)) (stepResult[T], error) {
	return dbos.RunAsStep(ctx, func(stepCtx context.Context) (stepResult[T], error) {
		return retrySync(stepCtx, logger, name, fn)
	}, dbos.WithStepName(name))
}

// retrySync calls fn until it succeeds, its error ends the sync, or
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
			logger.WarnContext(ctx, "sync step failed", "step", name, "err", err)
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

// runSync runs account input.AccountID's sync: a poll once its full sync is
// complete, going on to a full sync when the provider no longer keeps the
// changes asked for, and otherwise a full sync. It returns what it did, and
// logs to logger, by account ID, how a sync that did not succeed ended. It
// returns an error wrapping errSyncFailed when it failed, an error recording
// the end, or the context's error when the worker is stopping.
func runSync(ctx dbos.Context, full fullSyncs, polls polls, input SyncInput, logger *slog.Logger) (SyncOutput, error) {
	accountID := input.AccountID
	logger = logger.With("account_id", accountID)

	// 1. Choose the kind from the sync state, once: a resumed run keeps it.
	planned, err := syncStep(ctx, logger, "plan", func(stepCtx context.Context) (mailsync.Plan, error) {
		return polls.Plan(stepCtx, accountID)
	})
	if err != nil {
		return SyncOutput{}, err
	}
	switch planned.Ending {
	case 0:
	case mailsync.EndingFail:
		logger.WarnContext(ctx, "sync failed", "failure", planned.Failure)
		return SyncOutput{}, fmt.Errorf("%w: plan: %s", errSyncFailed, planned.Failure)
	default:
		logger.InfoContext(ctx, "sync ended", "outcome", "gone")
		return SyncOutput{}, nil
	}

	// 2. Poll, and go on to a full sync only when the changes are gone.
	if planned.Value.Kind == mailsync.KindPoll {
		polled, resync, err := runPoll(ctx, polls, accountID, planned.Value.Position, logger)
		if !resync {
			return polled, err
		}
		logger.InfoContext(ctx, "poll needs a full sync", "outcome", "resync")
	}
	status, err := runFullSync(ctx, full, polls, accountID, logger)
	return SyncOutput{Kind: mailsync.KindFull, Status: status}, err
}

// runPoll applies account accountID's changes since position, and returns
// what it did, logging to logger how a poll that did not succeed ended. It
// reports true, with nothing done, when the provider no longer keeps those
// changes, so a full sync must run instead. It returns an error wrapping
// errSyncFailed when it failed, an error recording the failure, or the
// context's error when the worker is stopping.
func runPoll(ctx dbos.Context, polls polls, accountID int64, position string, logger *slog.Logger) (SyncOutput, bool, error) {
	polled, err := pollPages(ctx, polls, accountID, mailsync.PollFrom{Scope: mailsync.PollComplete, Position: position}, logger)
	if err != nil {
		return SyncOutput{}, false, err
	}
	if polled.ending != 0 {
		return endPollStep(ctx, polls, accountID, position, polled.ending, polled.failure, logger)
	}
	return SyncOutput{Kind: mailsync.KindPoll, Status: mailsync.StatusComplete}, false, nil
}

// polled is how a poll's pages went: the position after the last one, or
// how a step's error ended the poll.
type polled struct {
	position string
	ending   mailsync.Ending
	failure  mailsync.Failure
}

// pollPages applies account accountID's changes since from's position, page
// by page, each read once and then applied part by part, and returns the
// position after them, or how a step's error ended the poll. It returns an
// error only when the worker is stopping.
//
// DESIGN: Each page is recorded by its read step, IDs only, so its parts
// apply exactly what was read however the provider's later answers differ.
func pollPages(ctx dbos.Context, polls polls, accountID int64, from mailsync.PollFrom, logger *slog.Logger) (polled, error) {
	pageToken := ""
	for {
		// 1. Read the page.
		read, err := syncStep(ctx, logger, "read changes", func(stepCtx context.Context) (mailsync.ChangePage, error) {
			return polls.ReadChanges(stepCtx, accountID, from.Position, pageToken)
		})
		if err != nil {
			return polled{}, err
		}
		if read.Ending != 0 {
			return polled{ending: read.Ending, failure: read.Failure}, nil
		}
		page := read.Value

		// 2. Apply it part by part.
		for part, more := 0, true; more; part++ {
			applied, err := syncStep(ctx, logger, "apply changes", func(stepCtx context.Context) (bool, error) {
				return polls.ApplyChanges(stepCtx, accountID, from, pageToken, page, part)
			})
			if err != nil {
				return polled{}, err
			}
			if applied.Ending != 0 {
				return polled{ending: applied.Ending, failure: applied.Failure}, nil
			}
			more = applied.Value
		}

		// 3. Go on to the next page, if any.
		if page.NextPageToken == "" {
			return polled{position: page.Position}, nil
		}
		pageToken = page.NextPageToken
	}
}

// endPollStep ends account accountID's poll from position as ending says,
// with failure for [mailsync.EndingFail], and returns what [runPoll]
// returns.
func endPollStep(ctx dbos.Context, polls polls, accountID int64, position string, ending mailsync.Ending, failure mailsync.Failure, logger *slog.Logger) (SyncOutput, bool, error) {
	done := SyncOutput{Kind: mailsync.KindPoll, Status: mailsync.StatusComplete}
	switch ending {
	case mailsync.EndingResync:
		return SyncOutput{}, true, nil
	case mailsync.EndingFail:
		return done, false, endPoll(ctx, polls, accountID, position, failure, logger)
	case mailsync.EndingGone:
		logger.InfoContext(ctx, "poll ended", "outcome", "gone")
		return SyncOutput{Kind: mailsync.KindPoll}, false, nil
	default:
		// A poll leaves the account complete; the account shows needing a
		// reconnect itself, and a deactivated member's next poll waits for
		// reactivation.
		logger.InfoContext(ctx, "poll ended", "outcome", mailsync.StatusWaiting)
		return done, false, nil
	}
}

// endPoll records that account accountID's poll from position failed with
// failure, unless the worker is stopping, logs it to logger, and returns an
// error wrapping errSyncFailed, joined with any error recording it.
func endPoll(ctx dbos.Context, polls polls, accountID int64, position string, failure mailsync.Failure, logger *slog.Logger) error {
	// 1. Record nothing while the worker is stopping: the next one resumes
	// at the step.
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// 2. Record the failure.
	logger.WarnContext(ctx, "poll failed", "failure", failure)
	_, err := dbos.RunAsStep(ctx, func(stepCtx context.Context) (struct{}, error) {
		return struct{}{}, polls.Fail(stepCtx, accountID, position, failure)
	}, append(syncRetries.options(), dbos.WithStepName("poll fail"))...)
	return errors.Join(fmt.Errorf("%w: poll: %s", errSyncFailed, failure), err)
}

// runFullSync runs account accountID's full sync, polling with polls every
// pollEveryPages pages of the copy, and returns the status it left:
// complete, waiting, failed, or empty when the account went or a newer full
// sync took over. It logs to logger how a full sync that did not complete
// ended. It returns an error wrapping errSyncFailed when it failed, an error
// recording the end, or the context's error when the worker is stopping.
func runFullSync(ctx dbos.Context, syncs fullSyncs, polls polls, accountID int64, logger *slog.Logger) (mailsync.Status, error) {
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
	// comes from the step before. Every pollEveryPages pages, poll, so mail
	// that arrived meanwhile need not wait for the copy to end.
	pageToken := ""
	position, polling := run.Position, true
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
		if !polling || (page+1)%pollEveryPages != 0 {
			continue
		}
		from := mailsync.PollFrom{Scope: mailsync.PollDuringCopy, Position: position, Generation: run.Generation}
		result, err := pollPages(ctx, polls, accountID, from, logger)
		if err != nil {
			return "", err
		}
		switch result.ending {
		case 0:
			position = result.position
		case mailsync.EndingResync:
			// Only a copy running past the provider's history, over a week,
			// gets here; the poll after it starts the next full sync.
			logger.InfoContext(ctx, "polls during the full sync stopped", "outcome", "resync")
			polling = false
		default:
			return endFullSync(ctx, syncs, accountID, run, result.ending, result.failure, logger)
		}
	}

	// 3. Delete what the provider no longer has, only once every page is
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
// error wrapping errSyncFailed, joined with any error recording it.
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
		return mailsync.StatusFailed, errors.Join(fmt.Errorf("%w: full: %s", errSyncFailed, failure), err)
	}
}
