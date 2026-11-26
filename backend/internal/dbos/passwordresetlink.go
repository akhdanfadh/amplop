package dbos

import (
	"context"
	"log/slog"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"amplop/internal/passwordreset"
)

// passwordResetLinkWorkflow names the reset link job in DBOS's tables, where
// enqueued runs outlive the code that made them. Its runs use [emailQueue].
const passwordResetLinkWorkflow = "amplop.password-reset-link"

// PasswordResetLinkInput is the reset link workflow's input: the request's
// attempt and the ID of the account with the requested email, 0 for none.
// The request looked the ID up, so the input holds no email, and the step
// reads the address when it runs.
type PasswordResetLinkInput struct {
	AttemptID int64
	UserID    int64
}

// passwordResetLinks saves and sends reset links, e.g.
// [passwordreset.Links].
type passwordResetLinks interface {
	Send(ctx context.Context, attemptID, userID int64) (passwordreset.Outcome, error)
}

// linkRetries spans about 5 minutes, 1, 2, then 2 minutes: the member is
// waiting on the page, and after that asking again does better than a late
// link. Each retry saves a new link, which counts toward the account's
// limits. Like every policy here, it holds for one run of a worker.
var linkRetries = retries{attempts: 3, base: time.Minute, ceiling: 2 * time.Minute}

// registerPasswordResetLink registers the workflow that sends reset links
// with links, or nothing when links is nil.
func (w *Worker) registerPasswordResetLink(links passwordResetLinks) {
	if links == nil {
		return
	}
	run := func(ctx dbos.Context, input PasswordResetLinkInput) (passwordreset.Outcome, error) {
		return runPasswordResetLink(ctx, links, input, w.logger)
	}
	dbos.RegisterWorkflow(w.ctx, run, dbos.WithWorkflowName(passwordResetLinkWorkflow))
}

// runPasswordResetLink sends the link input's request asked for, if its
// account may reset, logs the outcome to logger by attempt and user ID,
// adding no address itself, and returns it, or the send's error once its
// retries run out.
func runPasswordResetLink(ctx dbos.Context, links passwordResetLinks, input PasswordResetLinkInput, logger *slog.Logger) (passwordreset.Outcome, error) {
	// One step saves the link and sends it: the secret it makes may not be
	// recorded for a later step. It is not repeat-safe: one that succeeded
	// just before a crash runs again, as does an unconfirmed one, which is
	// retried on purpose; a second working link costs less than none.
	outcome, err := dbos.RunAsStep(ctx, func(stepCtx context.Context) (passwordreset.Outcome, error) {
		return links.Send(stepCtx, input.AttemptID, input.UserID)
	}, append(linkRetries.options(), dbos.WithStepName("send link"))...)
	if ctx.Err() != nil {
		// The worker is stopping; the next one resumes at this step.
		return "", ctx.Err()
	}
	if err != nil {
		logger.WarnContext(ctx, "password reset link failed", "attempt_id", input.AttemptID, "user_id", input.UserID, "err", err)
		return "", err
	}
	level := slog.LevelInfo
	if outcome.Failed() {
		level = slog.LevelWarn
	}
	logger.Log(ctx, level, "password reset link", "attempt_id", input.AttemptID, "user_id", input.UserID, "outcome", outcome)
	return outcome, nil
}
