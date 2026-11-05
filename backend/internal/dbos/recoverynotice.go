package dbos

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"inbok/internal/account"
)

// recoveryNoticeWorkflow names the recovery notice in DBOS's tables, where
// enqueued runs outlive the code that made them. Its runs use [emailQueue].
const recoveryNoticeWorkflow = "inbok.recovery-notice"

// RecoveryNoticeInput is the recovery notice workflow's input: the
// recovered administrator's ID and when the recovery happened. Steps read
// addresses when they run, so the input holds no personal data and stays
// valid if an address changes.
type RecoveryNoticeInput struct {
	UserID      int64
	RecoveredAt time.Time
}

// recoveryNotices lists and sends recovery notices, e.g.
// [account.RecoveryNotices].
type recoveryNotices interface {
	Administrators(ctx context.Context) ([]int64, error)
	Send(ctx context.Context, recoveredID int64, recoveredAt time.Time, recipientID int64) (account.NoticeOutcome, error)
}

// Retry policies of the recovery notice's steps.
var (
	// listRetries spans about 5 minutes, 1, 2, 4, 8, 16, 32 seconds, then 1
	// minute 4 times: a blip passes at once, and a database restart or
	// failover in time. The run sends nothing if the list fails.
	listRetries = retries{attempts: 10, base: time.Second, ceiling: time.Minute}

	// sendRetries spans about half an hour, 1, 2, 4, 8, then 15 minutes,
	// enough for a mail server's restart or a short outage.
	sendRetries = retries{attempts: 5, base: time.Minute, ceiling: 15 * time.Minute}
)

// recipientOutcome is how the notice to one administrator ended, as the
// workflow's output records it.
type recipientOutcome struct {
	UserID  int64
	Outcome account.NoticeOutcome
}

// registerRecoveryNotice registers the workflow that sends recovery notices
// with notices, or nothing when notices is nil.
//
// DESIGN: one step lists the administrators, then one step sends to each.
// DBOS records the list, by ID only, and each finished send, so a resumed
// run sends to the same administrators and skips those already done. A
// send is not repeat-safe: one that succeeded just before a crash runs
// again, as does an unconfirmed one, which is retried on purpose; a second
// copy of this notice costs less than none.
func (w *Worker) registerRecoveryNotice(notices recoveryNotices) {
	if notices == nil {
		return
	}
	run := func(ctx dbos.Context, input RecoveryNoticeInput) ([]recipientOutcome, error) {
		return runRecoveryNotice(ctx, notices, input, w.logger)
	}
	dbos.RegisterWorkflow(w.ctx, run, dbos.WithWorkflowName(recoveryNoticeWorkflow))
}

// runRecoveryNotice lists the active administrators, then sends each the
// notice of input's recovery, the recovered one first. It logs each outcome
// to logger by user ID, adding no address itself, and returns them. A send
// that runs out of retries does not stop the others; it returns their
// errors joined, or the list's error.
func runRecoveryNotice(ctx dbos.Context, notices recoveryNotices, input RecoveryNoticeInput, logger *slog.Logger) ([]recipientOutcome, error) {
	// 1. List the administrators. DBOS records the list, so a resumed run
	// gets this one back instead of listing again.
	admins, err := dbos.RunAsStep(ctx, notices.Administrators,
		append(listRetries.options(), dbos.WithStepName("list administrators"))...)
	if ctx.Err() != nil {
		// The worker is stopping; the next one resumes at this step.
		return nil, ctx.Err()
	}
	if err != nil {
		logger.WarnContext(ctx, "recovery notice failed", "recovered_id", input.UserID, "err", err)
		return nil, err
	}

	// 2. Put the recovered administrator first: their notice says what to
	// do, so it should not wait behind the others' retries. The list is
	// recorded, so this order is the same on every resume.
	if i := slices.Index(admins, input.UserID); i > 0 {
		admins = slices.Insert(slices.Delete(admins, i, i+1), 0, input.UserID)
	}

	// 3. Send to each in turn. A resumed run gets the finished sends' outcomes
	// back from DBOS and runs only the rest.
	var outcomes []recipientOutcome
	var failures []error
	for _, admin := range admins {
		outcome, err := dbos.RunAsStep(ctx, func(stepCtx context.Context) (account.NoticeOutcome, error) {
			return notices.Send(stepCtx, input.UserID, input.RecoveredAt, admin)
		}, append(sendRetries.options(), dbos.WithStepName("send notice"))...)
		if ctx.Err() != nil {
			// The worker is stopping; the next one resumes at this send.
			return outcomes, ctx.Err()
		}
		if err != nil {
			logger.WarnContext(ctx, "recovery notice failed", "recovered_id", input.UserID, "user_id", admin, "err", err)
			failures = append(failures, err)
			continue
		}

		level := slog.LevelInfo
		if outcome != account.NoticeSent {
			level = slog.LevelWarn
		}
		logger.Log(ctx, level, "recovery notice", "recovered_id", input.UserID, "user_id", admin, "outcome", outcome)
		outcomes = append(outcomes, recipientOutcome{UserID: admin, Outcome: outcome})
	}

	// 4. Fail the run if any send gave up, after every other was tried.
	return outcomes, errors.Join(failures...)
}
