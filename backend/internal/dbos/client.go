package dbos

import (
	"context"
	"log/slog"
	"time"
	"uuid"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"inbok/internal/connectedaccount"
	"inbok/internal/logging"
	"inbok/internal/outgoingmessage"
	"inbok/internal/passwordreset"
)

// Client saves workflows for a worker to run, from a process that runs
// none itself, e.g. inbok recover-admin or serve. It implements
// postgres.Workflows.
type Client struct {
	client dbos.Client
}

// NewClient opens a DBOS client on the database at url, without migrating
// it, and logs to logger. It returns an error if url is malformed or the
// workflow tables are missing or older than the SDK needs, which inbok
// migrate fixes.
func NewClient(ctx context.Context, url string, logger *slog.Logger) (*Client, error) {
	// 1. Parse url with pgx. DBOS's error for a malformed URL quotes it,
	// password included; pgx's hides the password.
	if _, err := pgxpool.ParseConfig(url); err != nil {
		return nil, err
	}

	// 2. Open the DBOS client on the URL pgx accepted.
	client, err := dbos.NewClient(ctx, dbos.ClientConfig{
		DatabaseURL: url,
		AppName:     appName,
		// DBOS logs the URL, hiding only a password written before the host.
		Logger: logging.WithoutAttrs(logger, "database_url"),
	})
	if err != nil {
		return nil, err
	}
	return &Client{client: client}, nil
}

// EnqueueRecoveryNotice saves, in tx, the recovery notice workflow for the
// recovery of the user with userID at recoveredAt on the email queue, so it
// commits or rolls back with tx. Cancelling ctx stops its statements. It
// returns DBOS's error, after which tx is aborted and must roll back.
func (c *Client) EnqueueRecoveryNotice(ctx context.Context, tx pgx.Tx, userID int64, recoveredAt time.Time) error {
	input := RecoveryNoticeInput{UserID: userID, RecoveredAt: recoveredAt}
	_, err := dbos.Enqueue[[]recipientOutcome](c.client, emailQueue, recoveryNoticeWorkflow, input,
		dbos.WithEnqueueTransaction(callerTx{Tx: tx, ctx: ctx}),
		// A worker resumes and dequeues only work of its own version.
		dbos.WithEnqueueApplicationVersion(applicationVersion),
	)
	return err
}

// EnqueuePasswordResetLink saves, in tx, the reset link workflow for the
// request with attemptID and the account with userID, 0 for none, on the
// email queue, so it commits or rolls back with tx. Cancelling ctx stops
// its statements. It returns DBOS's error, after which tx is aborted and
// must roll back.
func (c *Client) EnqueuePasswordResetLink(ctx context.Context, tx pgx.Tx, attemptID, userID int64) error {
	input := PasswordResetLinkInput{AttemptID: attemptID, UserID: userID}
	_, err := dbos.Enqueue[passwordreset.Outcome](c.client, emailQueue, passwordResetLinkWorkflow, input,
		dbos.WithEnqueueTransaction(callerTx{Tx: tx, ctx: ctx}),
		dbos.WithEnqueueApplicationVersion(applicationVersion),
	)
	return err
}

// EnqueuePasswordChangedNotice saves, in tx, the password changed notice
// workflow for the user with userID and the change at changedAt on the
// email queue, so it commits or rolls back with tx. Cancelling ctx stops
// its statements. It returns DBOS's error, after which tx is aborted and
// must roll back.
func (c *Client) EnqueuePasswordChangedNotice(ctx context.Context, tx pgx.Tx, userID int64, changedAt time.Time) error {
	input := PasswordChangedNoticeInput{UserID: userID, ChangedAt: changedAt}
	_, err := dbos.Enqueue[passwordreset.Outcome](c.client, emailQueue, passwordChangedNoticeWorkflow, input,
		dbos.WithEnqueueTransaction(callerTx{Tx: tx, ctx: ctx}),
		dbos.WithEnqueueApplicationVersion(applicationVersion),
	)
	return err
}

// EnqueueAccountRemoval saves, in tx, the account removal workflow for the
// removal with removalID on the Google queue, so it commits or rolls
// back with tx. Cancelling ctx stops its statements. It returns DBOS's
// error, after which tx is aborted and must roll back.
func (c *Client) EnqueueAccountRemoval(ctx context.Context, tx pgx.Tx, removalID int64) error {
	input := AccountRemovalInput{RemovalID: removalID}
	_, err := dbos.Enqueue[connectedaccount.RevokeOutcome](c.client, googleQueue, accountRemovalWorkflow, input,
		dbos.WithEnqueueTransaction(callerTx{Tx: tx, ctx: ctx}),
		dbos.WithEnqueueApplicationVersion(applicationVersion),
	)
	return err
}

// EnqueueOutgoingMessage saves, in tx, the send workflow for the outgoing
// message with messageID on the Google send queue, so it commits or rolls
// back with tx. The workflow's ID is the message's, so the message has one
// send. Cancelling ctx stops its statements. It returns DBOS's error, after
// which tx is aborted and must roll back.
func (c *Client) EnqueueOutgoingMessage(ctx context.Context, tx pgx.Tx, messageID uuid.UUID) error {
	input := OutgoingMessageInput{MessageID: messageID}
	_, err := dbos.Enqueue[outgoingmessage.Status](c.client, googleSendQueue, outgoingMessageWorkflow, input,
		dbos.WithEnqueueTransaction(callerTx{Tx: tx, ctx: ctx}),
		dbos.WithEnqueueWorkflowID(outgoingMessageWorkflowID(messageID)),
		dbos.WithEnqueueApplicationVersion(applicationVersion),
	)
	return err
}

// EnqueueSync saves, in tx, the sync workflow for the connected account
// with accountID on the Google queue, so it commits or rolls back with tx.
// Cancelling ctx stops its statements. It returns DBOS's error, wrapping
// [dbos.ErrQueueDeduplicated] when the account's sync is already queued or
// running, after which tx is aborted and must roll back.
func (c *Client) EnqueueSync(ctx context.Context, tx pgx.Tx, accountID int64) error {
	input := SyncInput{AccountID: accountID}
	_, err := dbos.Enqueue[SyncOutput](c.client, googleQueue, syncWorkflow, input,
		dbos.WithEnqueueTransaction(callerTx{Tx: tx, ctx: ctx}),
		dbos.WithEnqueueApplicationVersion(applicationVersion),
		dbos.WithEnqueueDeduplicationID(syncDeduplication(accountID)),
	)
	return err
}

// callerTx is a transaction whose statements run with ctx, whatever context
// they are given.
//
// NOTE: DBOS runs an enqueue's statements in the caller's transaction with
// a context that cannot be cancelled, so a lock held elsewhere, e.g. by a
// migration, would hold an interrupted recover-admin, and its database
// close, until released. With the caller's transaction DBOS neither retries
// nor commits, so a cancelled statement just fails the enqueue and the
// transaction rolls back. DBOS documents neither the uncancellable context
// nor a way around it: this relies on its adapter running every statement
// through Exec, Query, or QueryRow (v1.8.0), which
// TestClientRecoveryCancelled checks on each upgrade.
type callerTx struct {
	pgx.Tx
	ctx context.Context
}

func (t callerTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return t.Tx.Exec(t.ctx, sql, args...)
}

func (t callerTx) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	return t.Tx.Query(t.ctx, sql, args...)
}

func (t callerTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	return t.Tx.QueryRow(t.ctx, sql, args...)
}

// Close stops the client, waiting up to stageTimeout for each stage, and
// returns DBOS's error.
func (c *Client) Close() error {
	return dbos.Shutdown(c.client, stageTimeout)
}
