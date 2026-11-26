package dbos

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"amplop/internal/connectedaccount"
	"amplop/internal/logging"
	"amplop/internal/outgoingmessage"
	"amplop/internal/passwordreset"
)

// Client saves workflows for a worker to run in a caller's transaction,
// e.g. from amplop recover-admin, serve, or a worker's own step, whose
// workflow context DBOS refuses in a caller's transaction. It implements
// postgres.Workflows.
type Client struct {
	client dbos.Client
	// url is the database's, for the transactions the client opens itself.
	url string
}

// NewClient opens a DBOS client on the database at url, without migrating
// it, and logs to logger. It returns an error if url is malformed or the
// workflow tables are missing or older than the SDK needs, which amplop
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
	return &Client{client: client, url: url}, nil
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

// EnqueueContentDeletion saves, in tx, the content deletion workflow for
// the blobs with blobIDs of the account with accountID on the content
// queue, so it commits or rolls back with tx. Cancelling ctx stops its
// statements. It returns DBOS's error, after which tx is aborted and must
// roll back.
func (c *Client) EnqueueContentDeletion(ctx context.Context, tx pgx.Tx, accountID int64, blobIDs []uuid.UUID) error {
	input := ContentDeletionInput{AccountID: accountID, BlobIDs: blobIDs}
	_, err := dbos.Enqueue[int](c.client, contentQueue, contentDeletionWorkflow, input,
		dbos.WithEnqueueTransaction(callerTx{Tx: tx, ctx: ctx}),
		dbos.WithEnqueueApplicationVersion(applicationVersion),
	)
	return err
}

// EnqueueSearchIndexing saves, in tx, the search indexing workflow for
// member owner's messages with ids on the search queue, in owner's
// partition, so it commits or rolls back with tx. Cancelling ctx stops its
// statements. It returns DBOS's error, after which tx is aborted and must
// roll back.
func (c *Client) EnqueueSearchIndexing(ctx context.Context, tx pgx.Tx, owner int64, ids []uuid.UUID) error {
	input := SearchIndexingInput{OwnerUserID: owner, MessageIDs: ids}
	_, err := dbos.Enqueue[int](c.client, searchQueue, searchIndexingWorkflow, input,
		dbos.WithEnqueueTransaction(callerTx{Tx: tx, ctx: ctx}),
		dbos.WithEnqueueApplicationVersion(applicationVersion),
		dbos.WithEnqueueQueuePartitionKey(searchPartition(owner)),
	)
	return err
}

// EnqueueSearchAccountRemoval saves, in tx, the search account removal
// workflow for member owner's account accountID on the search queue, in
// owner's partition, so it commits or rolls back with tx. Cancelling ctx
// stops its statements. It returns DBOS's error, after which tx is aborted
// and must roll back.
func (c *Client) EnqueueSearchAccountRemoval(ctx context.Context, tx pgx.Tx, owner, accountID int64) error {
	input := SearchAccountRemovalInput{OwnerUserID: owner, AccountID: accountID}
	_, err := dbos.Enqueue[struct{}](c.client, searchQueue, searchAccountRemovalWorkflow, input,
		dbos.WithEnqueueTransaction(callerTx{Tx: tx, ctx: ctx}),
		dbos.WithEnqueueApplicationVersion(applicationVersion),
		dbos.WithEnqueueQueuePartitionKey(searchPartition(owner)),
	)
	return err
}

// EnqueueAccountContentDeletion saves, in tx, the workflow that deletes
// all content of the disconnected account with accountID on the content
// queue, so it commits or rolls back with tx. Cancelling ctx stops its
// statements. It returns DBOS's error, after which tx is aborted and must
// roll back.
func (c *Client) EnqueueAccountContentDeletion(ctx context.Context, tx pgx.Tx, accountID int64) error {
	input := AccountContentDeletionInput{AccountID: accountID}
	_, err := dbos.Enqueue[struct{}](c.client, contentQueue, accountContentDeletionWorkflow, input,
		dbos.WithEnqueueTransaction(callerTx{Tx: tx, ctx: ctx}),
		dbos.WithEnqueueApplicationVersion(applicationVersion),
	)
	return err
}

// EnqueueSync saves, in a savepoint of tx, the sync workflow for the
// connected account with accountID on the Google queue, so it commits or
// rolls back with tx. Cancelling ctx stops its statements. It reports
// false, saving nothing and leaving tx usable, when the account's sync is
// already queued or running. It returns DBOS's other errors, after which
// tx is aborted and must roll back.
//
// DESIGN: DBOS refuses its return-existing policy inside a caller's
// transaction, and a duplicate fails the insert, which aborts the
// transaction it ran in; the savepoint confines that to the enqueue.
func (c *Client) EnqueueSync(ctx context.Context, tx pgx.Tx, accountID int64) (bool, error) {
	// 1. Open the savepoint before DBOS's insert, which a duplicate fails.
	savepoint, err := tx.Begin(ctx)
	if err != nil {
		return false, err
	}

	// 2. Enqueue in it.
	input := SyncInput{AccountID: accountID}
	_, err = dbos.Enqueue[SyncOutput](c.client, googleQueue, syncWorkflow, input,
		dbos.WithEnqueueTransaction(callerTx{Tx: savepoint, ctx: ctx}),
		dbos.WithEnqueueApplicationVersion(applicationVersion),
		dbos.WithEnqueueDeduplicationID(syncDeduplication(accountID)),
	)

	// 3. Undo only the savepoint on a duplicate, which leaves tx usable.
	if errors.Is(err, dbos.ErrQueueDeduplicated) {
		return false, savepoint.Rollback(ctx)
	}
	if err != nil {
		return false, err
	}
	return true, savepoint.Commit(ctx)
}

// SyncScheduleSucceeded reports whether a run of the minute's sync
// schedule created after since succeeded. It stops when ctx ends, and
// returns DBOS's error, joined with ctx's when ctx ended first.
func (c *Client) SyncScheduleSucceeded(ctx context.Context, since time.Time) (bool, error) {
	// DBOS's calls run with its own context, not the one given, so the read
	// takes a copy of DBOS's that ctx's end cancels: a stalled read must not
	// outlive the request.
	//
	// NOTE: dbos.Client has no way to derive a context; the client
	// dbos.NewClient returns is a dbos.Context, which has (v1.8.0).
	// TestClientScoped checks it on each upgrade.
	scoped, ok := c.client.(dbos.Context)
	if !ok {
		return false, errNotScoped
	}
	scoped, cancel := dbos.WithTimeout(scoped, scheduleReadTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()

	runs, err := dbos.ListWorkflows(scoped,
		dbos.WithFilterScheduleName(syncSchedule),
		dbos.WithFilterStatus(dbos.WorkflowStatusSuccess),
		dbos.WithFilterCreatedAfter(since),
		dbos.WithFilterLimit(1),
		dbos.WithFilterLoadInput(false),
		dbos.WithFilterLoadOutput(false),
	)
	// The cancel above makes DBOS report a cancellation, so ctx's own
	// error goes with it: a request past its deadline then answers 503.
	if err != nil && ctx.Err() != nil {
		return false, errors.Join(ctx.Err(), err)
	}
	if err != nil {
		return false, err
	}
	return len(runs) > 0, nil
}

// RebuildSearch asks the worker to rebuild every member's search index, on
// the queue searchRebuildQueue, and waits until the rebuild ends, for at
// most timeout and until ctx ends. It returns what the rebuild did, or its
// error, which names the members whose rebuild failed. When it stops
// waiting first, the rebuild goes on in the worker, and it returns an
// error saying so; when it stops while still queueing it, the rebuild runs
// only if the queueing had been saved.
func (c *Client) RebuildSearch(ctx context.Context, timeout time.Duration) (SearchRebuildOutput, error) {
	// Every step runs with a copy of DBOS's context that timeout and ctx's
	// end cancel, as in SyncScheduleSucceeded.
	scoped, ok := c.client.(dbos.Context)
	if !ok {
		return SearchRebuildOutput{}, errNotScoped
	}
	scoped, cancel := dbos.WithTimeout(scoped, timeout)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()

	// 1. Queue the rebuild in a transaction of the client's own, which
	// scoped's end stops even while a lock elsewhere holds the insert, as
	// callerTx explains; DBOS's own transaction would wait for the lock
	// however long it is held.
	handle, err := c.enqueueRebuild(scoped)
	if err != nil && scoped.Err() != nil {
		return SearchRebuildOutput{}, fmt.Errorf("stopped queueing the search rebuild, which runs only if it was saved: %w", err)
	}
	if err != nil {
		return SearchRebuildOutput{}, err
	}

	// 2. Wait for it.
	output, err := handle.GetResult()
	if err != nil && scoped.Err() != nil {
		return SearchRebuildOutput{}, fmt.Errorf("stopped waiting for search rebuild %s, which goes on in the worker: %w", handle.GetWorkflowID(), err)
	}
	return output, err
}

// enqueueRebuild saves a rebuild of every member on searchRebuildQueue, in
// a transaction on a connection of its own, all with ctx, and returns its
// handle, which waits with ctx. It returns the connection's, the
// transaction's, or DBOS's error, after which nothing is saved unless the
// commit itself was cut short.
func (c *Client) enqueueRebuild(ctx dbos.Context) (dbos.WorkflowHandle[SearchRebuildOutput], error) {
	conn, err := pgx.Connect(ctx, c.url)
	if err != nil {
		return nil, fmt.Errorf("connect to queue the search rebuild: %w", err)
	}
	defer func() {
		// Not ctx, which may have ended: closing only says goodbye.
		closeCtx, cancel := context.WithTimeout(context.Background(), stageTimeout)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	handle, err := dbos.Enqueue[SearchRebuildOutput](ctx, searchRebuildQueue, searchRebuildWorkflow, struct{}{},
		dbos.WithEnqueueTransaction(callerTx{Tx: tx, ctx: ctx}),
		dbos.WithEnqueueApplicationVersion(applicationVersion),
	)
	if err != nil {
		// The connection closes next, which rolls back whatever is left.
		return nil, err
	}
	return handle, tx.Commit(ctx)
}

// errNotScoped means the DBOS client cannot take a deadline, which an SDK
// upgrade may cause.
var errNotScoped = errors.New("DBOS client cannot take a deadline")

// scheduleReadTimeout bounds [Client.SyncScheduleSucceeded] when its
// caller sets no shorter deadline, amplop's choice: under a request's
// 10-second work limit.
const scheduleReadTimeout = 5 * time.Second

// callerTx is a transaction whose statements run with ctx, whatever context
// they are given.
//
// NOTE: DBOS runs an enqueue's statements in the caller's transaction with
// a context that cannot be cancelled, so a lock held elsewhere, e.g. by a
// migration, would hold an interrupted recover-admin or search rebuild, and
// its database close, until released. With the caller's transaction DBOS neither retries
// nor commits, so a cancelled statement just fails the enqueue and the
// transaction rolls back. DBOS documents neither the uncancellable context
// nor a way around it: this relies on its adapter running every statement
// through Exec, Query, or QueryRow (v1.8.0), which
// TestClientRecoveryCancelled and TestClientRebuildCancelled check on each
// upgrade.
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
