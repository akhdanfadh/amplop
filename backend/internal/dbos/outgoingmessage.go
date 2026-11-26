package dbos

import (
	"context"
	"log/slog"
	"time"
	"uuid"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"inbok/internal/outgoingmessage"
)

// outgoingMessageWorkflow names the send job in DBOS's tables, where
// enqueued runs outlive the code that made them. Its runs use
// [googleSendQueue].
const outgoingMessageWorkflow = "inbok.outgoing-message"

// checkDelay is how long the send job waits after an unanswered send before
// looking for the message in Sent, so a request Gmail is still working on
// lands first. Google promises no bound; this is inbok's choice.
var checkDelay = 2 * time.Minute

// giveUpDelay is how long the send job waits before trying again to end a
// message whose give up step kept failing, e.g. while the database is down:
// the message must not stay waiting or sending for good. inbok's choice.
var giveUpDelay = 15 * time.Minute

// OutgoingMessageInput is the send workflow's input: the message to send.
// The steps read the message and a token when they run, so the input holds
// neither content nor secret.
type OutgoingMessageInput struct {
	MessageID uuid.UUID
}

// outgoingMessages sends saved messages, e.g. [outgoingmessage.Sending].
type outgoingMessages interface {
	Prepare(ctx context.Context, messageID uuid.UUID) (outgoingmessage.Preparation, error)
	Send(ctx context.Context, messageID uuid.UUID) (outgoingmessage.Status, error)
	Check(ctx context.Context, messageID uuid.UUID, historyID string) (outgoingmessage.Status, error)
	GiveUp(ctx context.Context, messageID uuid.UUID) (outgoingmessage.Status, error)
}

// sendRetries spans about a minute, 2, 4, 8, 16, then 30 seconds: the
// member is waiting on the page, and Gmail's guide asks for exponential
// backoff from at least a second. Like every policy here, it holds for one
// run of a worker.
var sendRetries = retries{attempts: 5, base: 2 * time.Second, ceiling: 30 * time.Second}

// checkRetries spans about 3 minutes, 30 seconds, 1, then 2 minutes: the
// member already waited for [checkDelay].
var checkRetries = retries{attempts: 3, base: 30 * time.Second, ceiling: 2 * time.Minute}

// outgoingMessageWorkflowID returns the send workflow's ID for the message
// messageID, so a message is sent by one workflow.
func outgoingMessageWorkflowID(messageID uuid.UUID) string {
	return outgoingMessageWorkflow + "/" + messageID.String()
}

// registerOutgoingMessage registers the workflow that sends messages with
// messages, or nothing when messages is nil, as when no Google client is
// configured.
func (w *Worker) registerOutgoingMessage(messages outgoingMessages) {
	if messages == nil {
		return
	}
	run := func(ctx dbos.Context, input OutgoingMessageInput) (outgoingmessage.Status, error) {
		return runOutgoingMessage(ctx, messages, input, w.logger)
	}
	dbos.RegisterWorkflow(w.ctx, run, dbos.WithWorkflowName(outgoingMessageWorkflow))
}

// runOutgoingMessage sends input's message, logs the status it ends with to
// logger by message ID, adding nothing of its content, and returns the status,
// empty when the message went. When a step keeps failing it ends the message
// with [outgoingmessage.Sending.GiveUp], trying until that is saved.
func runOutgoingMessage(ctx dbos.Context, messages outgoingMessages, input OutgoingMessageInput, logger *slog.Logger) (outgoingmessage.Status, error) {
	id := input.MessageID
	options := func(name string, policy retries) []dbos.StepOption {
		return append(policy.options(), dbos.WithStepName(name))
	}

	// 1. Note Gmail's position before sending, which the check needs.
	prepared, err := dbos.RunAsStep(ctx, func(stepCtx context.Context) (outgoingmessage.Preparation, error) {
		return messages.Prepare(stepCtx, id)
	}, options("prepare send", sendRetries)...)
	if ctx.Err() != nil {
		// The worker is stopping; the next one resumes at this step.
		return "", ctx.Err()
	}
	if err != nil {
		return giveUp(ctx, messages, id, err, logger)
	}
	if prepared.Status != outgoingmessage.StatusWaiting {
		return logOutcome(ctx, logger, id, prepared.Status), nil
	}

	// 2. Send. A step run again after a crash finds the message sending
	// and leaves it for the check.
	status, err := dbos.RunAsStep(ctx, func(stepCtx context.Context) (outgoingmessage.Status, error) {
		return messages.Send(stepCtx, id)
	}, options("send message", sendRetries)...)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return giveUp(ctx, messages, id, err, logger)
	}
	if status != outgoingmessage.StatusSending {
		return logOutcome(ctx, logger, id, status), nil
	}

	// 3. Unanswered: wait, then look for the message in Sent.
	if _, err := dbos.Sleep(ctx, checkDelay); err != nil {
		return "", err
	}
	status, err = dbos.RunAsStep(ctx, func(stepCtx context.Context) (outgoingmessage.Status, error) {
		return messages.Check(stepCtx, id, prepared.HistoryID)
	}, options("check sent", checkRetries)...)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return giveUp(ctx, messages, id, err, logger)
	}
	return logOutcome(ctx, logger, id, status), nil
}

// giveUp logs cause, the error a step kept returning, ends message id with
// [outgoingmessage.Sending.GiveUp], waiting [giveUpDelay] between rounds of
// retries until it is saved, and returns the status it ends with. It
// returns an error only when the worker stops.
//
// DESIGN: a run that ended with an error is not resumed, so returning one
// here would leave the message waiting or sending for good.
func giveUp(ctx dbos.Context, messages outgoingMessages, id uuid.UUID, cause error, logger *slog.Logger) (outgoingmessage.Status, error) {
	logger.WarnContext(ctx, "outgoing message step failed", "message_id", id, "err", cause)
	for {
		status, err := dbos.RunAsStep(ctx, func(stepCtx context.Context) (outgoingmessage.Status, error) {
			return messages.GiveUp(stepCtx, id)
		}, append(sendRetries.options(), dbos.WithStepName("give up"))...)
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if err == nil {
			return logOutcome(ctx, logger, id, status), nil
		}
		logger.WarnContext(ctx, "outgoing message give up failed", "message_id", id, "err", err)
		if _, err := dbos.Sleep(ctx, giveUpDelay); err != nil {
			return "", err
		}
	}
}

// logOutcome logs status, the one message id ended with, to logger, as a
// warning unless it was sent, and returns it.
func logOutcome(ctx context.Context, logger *slog.Logger, id uuid.UUID, status outgoingmessage.Status) outgoingmessage.Status {
	level := slog.LevelInfo
	if status != outgoingmessage.StatusSent {
		level = slog.LevelWarn
	}
	logger.Log(ctx, level, "outgoing message", "message_id", id, "status", status)
	return status
}
