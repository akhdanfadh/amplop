package dbos

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/connectedaccount"
	"inbok/internal/encryption"
)

// fakeRemovals answers every Revoke with outcome and err and every Discard
// with discardErr, and records the removal IDs it is asked to revoke and to
// discard.
type fakeRemovals struct {
	outcome    connectedaccount.RevokeOutcome
	err        error
	discardErr error

	mu        sync.Mutex
	revoked   []int64
	discarded []int64
}

func (f *fakeRemovals) Revoke(_ context.Context, removalID int64) (connectedaccount.RevokeOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, removalID)
	return f.outcome, f.err
}

func (f *fakeRemovals) Discard(_ context.Context, removalID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.discarded = append(f.discarded, removalID)
	return f.discardErr
}

// calls returns the IDs Revoke and Discard were called with, in order.
func (f *fakeRemovals) calls() ([]int64, []int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.revoked), slices.Clone(f.discarded)
}

// removalWorker launches a worker on url revoking tokens with
// removals.
func removalWorker(t *testing.T, url string, removals accountRemovals) *Worker {
	t.Helper()

	worker, err := NewWorker(url, "worker", Services{AccountRemovals: removals}, discard)
	require.NoError(t, err)
	require.NoError(t, worker.Launch())
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })
	return worker
}

// enqueueRemoval enqueues removal 7's job by name, as a client does,
// and returns its handle.
func enqueueRemoval(t *testing.T, worker *Worker) dbos.WorkflowHandle[connectedaccount.RevokeOutcome] {
	t.Helper()

	handle, err := dbos.Enqueue[connectedaccount.RevokeOutcome](worker.ctx, googleQueue, accountRemovalWorkflow,
		AccountRemovalInput{RemovalID: 7})
	require.NoError(t, err)
	return handle
}

func TestAccountRemoval(t *testing.T) {
	removals := &fakeRemovals{outcome: connectedaccount.RevokeRevoked}
	worker := removalWorker(t, migrated(t), removals)

	outcome, err := enqueueRemoval(t, worker).GetResult(dbos.WithHandleTimeout(wait))

	require.NoError(t, err)
	assert.Equal(t, connectedaccount.RevokeRevoked, outcome)
	revoked, discarded := removals.calls()
	assert.Equal(t, []int64{7}, revoked)
	assert.Empty(t, discarded)
}

// A removal Google keeps failing is retried with its policy, then
// discarded, so the sealed token is not kept; the run fails with its error.
func TestAccountRemovalGivesUp(t *testing.T) {
	quickRetries(t, &revokeRetries)
	removals := &fakeRemovals{err: errors.New("google unavailable")}
	worker := removalWorker(t, migrated(t), removals)

	_, err := enqueueRemoval(t, worker).GetResult(dbos.WithHandleTimeout(wait))

	require.ErrorContains(t, err, "google unavailable")
	revoked, discarded := removals.calls()
	assert.Len(t, revoked, 6, "the first attempt and 5 retries")
	assert.Equal(t, []int64{7}, discarded)
}

// A discard that keeps failing too is retried with the same policy, and
// the run fails with both errors, so a kept token does not go unnoticed.
func TestAccountRemovalDiscardFails(t *testing.T) {
	quickRetries(t, &revokeRetries)
	removals := &fakeRemovals{err: errors.New("google unavailable"), discardErr: errors.New("discard failed")}
	worker := removalWorker(t, migrated(t), removals)

	_, err := enqueueRemoval(t, worker).GetResult(dbos.WithHandleTimeout(wait))

	require.ErrorContains(t, err, "google unavailable")
	require.ErrorContains(t, err, "discard failed")
	_, discarded := removals.calls()
	assert.Len(t, discarded, 6, "the first attempt and 5 retries")
}

// Removals are retried 5 more times, waiting 1, 2, 4, 8, then 15
// minutes, as the backend README promises.
func TestRevokeRetryPolicy(t *testing.T) {
	assert.Equal(t, retries{attempts: 5, base: time.Minute, ceiling: 15 * time.Minute}, revokeRetries)
}

// accountRemovalWorkflows lists the account removal workflows saved in
// DBOS's tables, by status.
func accountRemovalWorkflows(t *testing.T, conn *pgx.Conn) []string {
	t.Helper()
	rows, err := conn.Query(t.Context(), "SELECT status FROM dbos.workflow_status WHERE name = $1", accountRemovalWorkflow)
	require.NoError(t, err)
	statuses, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	return statuses
}

// A worker without Google settings leaves removals queued, so a worker
// with them, under another ID, takes them.
func TestRemovalWaitsForGoogle(t *testing.T) {
	// Given a removal queued while a worker without Google runs.
	url := migrated(t)
	without, err := NewWorker(url, "without-google", Services{}, discard)
	require.NoError(t, err)
	require.NoError(t, without.Launch())
	t.Cleanup(func() { assert.NoError(t, without.Shutdown()) })
	handle, err := dbos.Enqueue[connectedaccount.RevokeOutcome](without.ctx, googleQueue, accountRemovalWorkflow,
		AccountRemovalInput{RemovalID: 7})
	require.NoError(t, err)

	// Then it stays queued.
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close(context.Background()) })
	assert.Never(t, func() bool {
		statuses := accountRemovalWorkflows(t, conn)
		return len(statuses) != 1 || statuses[0] != "ENQUEUED"
	}, 3*time.Second, 100*time.Millisecond)

	// When a worker with Google starts, it revokes it.
	removals := &fakeRemovals{outcome: connectedaccount.RevokeRevoked}
	with, err := NewWorker(url, "with-google", Services{AccountRemovals: removals}, discard)
	require.NoError(t, err)
	require.NoError(t, with.Launch())
	t.Cleanup(func() { assert.NoError(t, with.Shutdown()) })

	outcome, err := handle.GetResult(dbos.WithHandleTimeout(wait))
	require.NoError(t, err)
	assert.Equal(t, connectedaccount.RevokeRevoked, outcome)
}

// A disconnect saves the removal for the worker, whose input holds only
// the removal's ID, never the token.
func TestClientAccountRemoval(t *testing.T) {
	// Given a member with a connected account, through storage that saves
	// workflows with a Client.
	db, conn, url := clientStack(t)
	var member int64
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT id FROM users WHERE email = 'admin@example.com'").Scan(&member))
	ring, err := encryption.New("1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	require.NoError(t, err)
	accounts := connectedaccount.NewService(db, ring)
	account, err := accounts.Connect(t.Context(), member, connectedaccount.Identity{Subject: "100000000000000000001", Email: "alex@example.com"}, "refresh-secret-1")
	require.NoError(t, err)

	// When the member disconnects it.
	_, err = accounts.Disconnect(t.Context(), member, account.ID)
	require.NoError(t, err)

	// Then its removal waits in the Google queue, holding no token or
	// email, until a worker revokes it.
	assert.Equal(t, []string{"ENQUEUED"}, accountRemovalWorkflows(t, conn))
	var input, queue string
	require.NoError(t, conn.QueryRow(t.Context(), `SELECT i.inputs, s.queue_name FROM dbos.workflow_input i
		JOIN dbos.workflow_status s USING (workflow_uuid) WHERE s.name = $1`, accountRemovalWorkflow).Scan(&input, &queue))
	assert.Equal(t, googleQueue, queue)
	decoded, err := base64.StdEncoding.DecodeString(input)
	require.NoError(t, err)
	assert.Contains(t, string(decoded), `"RemovalID"`)
	assert.NotContains(t, string(decoded), "refresh")
	assert.NotContains(t, string(decoded), "@")
	removals := &fakeRemovals{outcome: connectedaccount.RevokeRevoked}
	removalWorker(t, url, removals)
	require.Eventually(t, func() bool { revoked, _ := removals.calls(); return len(revoked) == 1 }, wait, 50*time.Millisecond)
}

// A disconnect that fails as it commits rolls its removal job back with
// it, and keeps the account.
func TestClientRemovalRollback(t *testing.T) {
	// Given a connected account, and removals that fail as they commit,
	// after the job is saved.
	db, conn, _ := clientStack(t)
	var member int64
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT id FROM users WHERE email = 'admin@example.com'").Scan(&member))
	ring, err := encryption.New("1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	require.NoError(t, err)
	accounts := connectedaccount.NewService(db, ring)
	account, err := accounts.Connect(t.Context(), member, connectedaccount.Identity{Subject: "100000000000000000001", Email: "alex@example.com"}, "refresh-secret-1")
	require.NoError(t, err)
	_, err = conn.Exec(t.Context(), `CREATE FUNCTION reject_removal() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'removal rejected'; END $$`)
	require.NoError(t, err)
	_, err = conn.Exec(t.Context(), "CREATE CONSTRAINT TRIGGER reject_write AFTER INSERT ON account_removals DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_removal()")
	require.NoError(t, err)

	// When the member disconnects it.
	_, err = accounts.Disconnect(t.Context(), member, account.ID)

	// Then the disconnect fails, the account stays, and no job is saved.
	require.ErrorContains(t, err, "removal rejected")
	listed, err := accounts.List(t.Context(), member)
	require.NoError(t, err)
	assert.Equal(t, []connectedaccount.Account{account}, listed)
	assert.Empty(t, accountRemovalWorkflows(t, conn))
}
