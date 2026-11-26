package dbos

import (
	"context"
	"log/slog"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"amplop/internal/passwordreset"
)

// passwordChangedNoticeWorkflow names the password changed notice in DBOS's
// tables, where enqueued runs outlive the code that made them. Its runs use
// [emailQueue].
const passwordChangedNoticeWorkflow = "amplop.password-changed-notice"

// PasswordChangedNoticeInput is the password changed notice workflow's
// input: the user whose password a reset link changed, and when. The step
// reads the address when it runs, so the input holds no personal data.
type PasswordChangedNoticeInput struct {
	UserID    int64
	ChangedAt time.Time
}

// passwordChangedNotices sends password changed notices, e.g.
// [passwordreset.Notices].
type passwordChangedNotices interface {
	Send(ctx context.Context, userID int64, changedAt time.Time) (passwordreset.Outcome, error)
}

// registerPasswordChangedNotice registers the workflow that sends password
// changed notices with notices, or nothing when notices is nil.
func (w *Worker) registerPasswordChangedNotice(notices passwordChangedNotices) {
	if notices == nil {
		return
	}
	run := func(ctx dbos.Context, input PasswordChangedNoticeInput) (passwordreset.Outcome, error) {
		return runPasswordChangedNotice(ctx, notices, input, w.logger)
	}
	dbos.RegisterWorkflow(w.ctx, run, dbos.WithWorkflowName(passwordChangedNoticeWorkflow))
}

// runPasswordChangedNotice sends the notice of input's change, logs the
// outcome to logger by user ID, adding no address itself, and returns it,
// or the send's error once its retries run out.
func runPasswordChangedNotice(ctx dbos.Context, notices passwordChangedNotices, input PasswordChangedNoticeInput, logger *slog.Logger) (passwordreset.Outcome, error) {
	// A send is not repeat-safe: one that succeeded just before a crash runs
	// again, as does an unconfirmed one, which is retried on purpose; a
	// second copy of this notice costs less than none.
	outcome, err := dbos.RunAsStep(ctx, func(stepCtx context.Context) (passwordreset.Outcome, error) {
		return notices.Send(stepCtx, input.UserID, input.ChangedAt)
	}, append(noticeRetries.options(), dbos.WithStepName("send notice"))...)
	if ctx.Err() != nil {
		// The worker is stopping; the next one resumes at this step.
		return "", ctx.Err()
	}
	if err != nil {
		logger.WarnContext(ctx, "password changed notice failed", "user_id", input.UserID, "err", err)
		return "", err
	}
	level := slog.LevelInfo
	if outcome.Failed() {
		level = slog.LevelWarn
	}
	logger.Log(ctx, level, "password changed notice", "user_id", input.UserID, "outcome", outcome)
	return outcome, nil
}
