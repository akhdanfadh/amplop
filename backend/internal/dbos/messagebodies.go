package dbos

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"amplop/internal/mailsync"
	"amplop/internal/messagecontent"
)

// The background body fetch's names in DBOS's tables, where enqueued runs
// and the schedule outlive the code that made them. Its runs use
// [googleQueue]; the schedule fires as [syncCron] does.
const (
	messageBodiesWorkflow         = "amplop.message-bodies"
	messageBodiesScheduleWorkflow = "amplop.message-bodies-schedule"
	messageBodiesSchedule         = "amplop.message-bodies.minutely"
)

// maxBodyBatches is the most batches one body fetch run takes, amplop's
// choice: about 1,000 messages, some 12 minutes at Gmail's pace when each
// message's text comes in its first answer, longer when text comes
// separately or calls are retried. The next minute's body schedule starts
// another while bodies are still due, so a run stays short to resume and
// holds its place in [googleQueue]'s limit, if one is set, for a bounded
// number of messages.
const maxBodyBatches = 50

// MessageBodiesInput is the body fetch workflow's input: the account
// whose messages' bodies it fetches.
//
// DESIGN: The account's data is the list of what to fetch: messages
// without parts. The input names no message, so a lost or repeated run
// changes nothing but when the bodies arrive.
type MessageBodiesInput struct {
	AccountID int64
}

// MessageBodiesOutput is what a body fetch run did: how many messages'
// content it saved, and how many fetches failed for good.
type MessageBodiesOutput struct {
	Saved  int
	Failed int
}

// bodyFetches picks the accounts whose bodies are due and fetches them,
// e.g. [messagecontent.Fetcher].
type bodyFetches interface {
	DueAccounts(ctx context.Context) ([]int64, error)
	FetchDue(ctx context.Context, accountID int64) (messagecontent.Batch, error)
}

// bodyStep is one batch's recorded result: the batch, and whether the
// account's provider work has stopped, e.g. it needs a reconnect.
type bodyStep struct {
	Batch   messagecontent.Batch
	Stopped bool
}

// messageBodiesDeduplication returns the deduplication ID of accountID's
// body fetch runs on [googleQueue]: DBOS refuses a second while one is
// queued or running, so an account has one at a time.
func messageBodiesDeduplication(accountID int64) string {
	return "message-bodies-" + strconv.FormatInt(accountID, 10)
}

// registerMessageBodies registers the body fetch workflow with bodies, or
// nothing when bodies is nil, as when no Google client is configured.
func (w *Worker) registerMessageBodies(bodies bodyFetches) {
	if bodies == nil {
		return
	}
	run := func(ctx dbos.Context, input MessageBodiesInput) (MessageBodiesOutput, error) {
		return runMessageBodies(ctx, bodies, input, w.logger)
	}
	dbos.RegisterWorkflow(w.ctx, run, dbos.WithWorkflowName(messageBodiesWorkflow))
}

// runMessageBodies fetches account input.AccountID's due bodies, a batch
// per step, until none are due, the account's provider work stops, or
// [maxBodyBatches] ran. It returns what it did, and a batch's error,
// logged to logger, which ends the run: the next minute's body schedule
// starts another.
// It returns the context's error when the worker is stopping.
func runMessageBodies(ctx dbos.Context, bodies bodyFetches, input MessageBodiesInput, logger *slog.Logger) (MessageBodiesOutput, error) {
	var output MessageBodiesOutput
	for range maxBodyBatches {
		step, err := dbos.RunAsStep(ctx, func(stepCtx context.Context) (bodyStep, error) {
			batch, err := bodies.FetchDue(stepCtx, input.AccountID)
			// An account that needs a reconnect, or went, ends the run as the
			// sync's would; the sync records why.
			if err != nil && stepCtx.Err() == nil && mailsync.EndingOf(err) != mailsync.EndingRetry {
				return bodyStep{Batch: batch, Stopped: true}, nil
			}
			return bodyStep{Batch: batch}, err
		}, dbos.WithStepName("fetch bodies"))
		if err != nil {
			if ctx.Err() == nil {
				logger.WarnContext(ctx, "message body fetch failed", "account_id", input.AccountID, "err", err)
			}
			return output, err
		}
		output.Saved += step.Batch.Saved
		output.Failed += step.Batch.Failed
		if step.Stopped || !step.Batch.More {
			break
		}
	}
	return output, nil
}

// registerMessageBodiesSchedule registers the workflow that starts the
// body fetches bodies picks, and schedules it every minute; with bodies
// nil, as when no Google client is configured, it deletes the schedule
// instead, which an earlier start may have saved. It returns DBOS's error
// saving or deleting the schedule.
//
// DESIGN: A schedule of its own, not a step of the sync schedule's run,
// whose success is the heartbeat that tells members syncing works: body
// work that stalls, e.g. behind a lock on message_parts, must not make
// healthy syncing look stopped.
func (w *Worker) registerMessageBodiesSchedule(bodies bodyFetches) error {
	if bodies == nil {
		return dbos.DeleteSchedule(w.ctx, messageBodiesSchedule)
	}
	run := func(ctx dbos.Context, _ dbos.ScheduledWorkflowInput) (int, error) {
		return startDueBodies(ctx, bodies, w.logger)
	}
	dbos.RegisterWorkflow(w.ctx, run, dbos.WithWorkflowName(messageBodiesScheduleWorkflow))
	return dbos.ApplySchedules(w.ctx, []dbos.ScheduleSpec{{
		ScheduleName: messageBodiesSchedule,
		Schedule:     syncCron,
		CronTimezone: "UTC",
		WorkflowName: messageBodiesScheduleWorkflow,
	}})
}

// startDueBodies starts a body fetch on [googleQueue] for each account
// bodies picks, keeping one queued or running, as steps of the schedule
// run ctx, and returns how many it asked for. It returns the error
// picking, logged to logger; an enqueue that fails is logged and the rest
// go on. Either is left for the next minute's run.
//
// DESIGN: A minute's schedule, rather than the end of a sync run or a job
// saved with each page: bodies never share the account's pace with a full
// sync's copy, and a quiet account, which gets no sync run, still has a
// run that ended at its limit or failed started again within a minute.
func startDueBodies(ctx dbos.Context, bodies bodyFetches, logger *slog.Logger) (int, error) {
	accounts, err := dbos.RunAsStep(ctx, func(stepCtx context.Context) ([]int64, error) {
		return bodies.DueAccounts(stepCtx)
	}, dbos.WithStepName("pick due bodies"))
	if err != nil {
		if ctx.Err() == nil {
			logger.WarnContext(ctx, "message body check failed", "err", err)
		}
		return 0, err
	}
	started := 0
	for _, accountID := range accounts {
		_, err := dbos.Enqueue[MessageBodiesOutput](ctx, googleQueue, messageBodiesWorkflow, MessageBodiesInput{AccountID: accountID},
			dbos.WithEnqueueDeduplicationID(messageBodiesDeduplication(accountID)),
			dbos.WithEnqueueDeduplicationPolicy(dbos.DeduplicationPolicyReturnExisting))
		if ctx.Err() != nil {
			return started, ctx.Err()
		}
		if err != nil {
			logger.WarnContext(ctx, "message body fetch not started", "account_id", accountID, "err", err)
			continue
		}
		started++
	}
	return started, nil
}
