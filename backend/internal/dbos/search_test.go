package dbos

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// searchCall is one call a [fakeSearch] took.
type searchCall struct {
	Owner     int64
	IDs       []uuid.UUID
	AccountID int64 // set for a removal
}

// fakeSearch records each call and answers err. A call for an owner in
// hold waits until its channel closes.
type fakeSearch struct {
	err  error
	hold map[int64]chan struct{}

	mu    sync.Mutex
	calls []searchCall
}

func (f *fakeSearch) Index(ctx context.Context, owner int64, ids []uuid.UUID) error {
	f.record(searchCall{Owner: owner, IDs: slices.Clone(ids)})
	if gate, ok := f.hold[owner]; ok {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.err
}

func (f *fakeSearch) RemoveAccount(_ context.Context, owner, accountID int64) error {
	f.record(searchCall{Owner: owner, AccountID: accountID})
	return f.err
}

func (f *fakeSearch) record(call searchCall) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

// state returns the calls so far.
func (f *fakeSearch) state() []searchCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// owners returns how many calls each owner made so far.
func (f *fakeSearch) owners() map[int64]int {
	counts := map[int64]int{}
	for _, call := range f.state() {
		counts[call.Owner]++
	}
	return counts
}

// searchWorker launches a worker on url writing to index.
func searchWorker(t *testing.T, url string, index searchIndex) *Worker {
	t.Helper()
	worker, err := NewWorker(url, "worker", Services{SearchIndex: index}, discard)
	require.NoError(t, err)
	require.NoError(t, worker.Launch())
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })
	return worker
}

// enqueueIndexing queues owner's indexing of ids in owner's partition, as
// the client does.
func enqueueIndexing(t *testing.T, worker *Worker, owner int64, ids []uuid.UUID) dbos.WorkflowHandle[int] {
	t.Helper()
	handle, err := dbos.Enqueue[int](worker.ctx, searchQueue, searchIndexingWorkflow,
		SearchIndexingInput{OwnerUserID: owner, MessageIDs: ids},
		dbos.WithEnqueueQueuePartitionKey(searchPartition(owner)))
	require.NoError(t, err)
	return handle
}

// newIDs returns n new message IDs.
func newIDs(n int) []uuid.UUID {
	ids := make([]uuid.UUID, n)
	for i := range ids {
		ids[i] = uuid.New()
	}
	return ids
}

// A run indexes its messages in batches of at most 100, in order, and
// returns how many it took.
func TestSearchIndexing(t *testing.T) {
	index := &fakeSearch{}
	worker := searchWorker(t, migrated(t), index)
	ids := newIDs(250)

	indexed, err := enqueueIndexing(t, worker, 7, ids).GetResult(dbos.WithHandleTimeout(wait))

	require.NoError(t, err)
	assert.Equal(t, 250, indexed)
	assert.Equal(t, []searchCall{
		{Owner: 7, IDs: ids[:100]},
		{Owner: 7, IDs: ids[100:200]},
		{Owner: 7, IDs: ids[200:]},
	}, index.state())
}

// A member's runs never overlap: the second waits until the first ends,
// while another member's runs alongside.
func TestSearchPartitions(t *testing.T) {
	release := make(chan struct{})
	index := &fakeSearch{hold: map[int64]chan struct{}{7: release}}
	worker := searchWorker(t, migrated(t), index)

	first := enqueueIndexing(t, worker, 7, newIDs(1))
	require.Eventually(t, func() bool { return index.owners()[7] == 1 }, wait, 20*time.Millisecond)
	second := enqueueIndexing(t, worker, 7, newIDs(1))
	other := enqueueIndexing(t, worker, 8, newIDs(1))

	_, otherErr := other.GetResult(dbos.WithHandleTimeout(wait))
	// The second had a second and more to start, as DBOS polls queues
	// about every second; it waits on the first instead.
	time.Sleep(1500 * time.Millisecond)
	waiting := index.owners()[7]
	close(release)
	_, firstErr := first.GetResult(dbos.WithHandleTimeout(wait))
	_, secondErr := second.GetResult(dbos.WithHandleTimeout(wait))

	require.NoError(t, otherErr)
	require.NoError(t, firstErr)
	require.NoError(t, secondErr)
	assert.Equal(t, 1, waiting, "the second run did not start while the first ran")
	assert.Equal(t, 2, index.owners()[7])
}

// An index that keeps failing is retried with its policy, and the run
// fails.
func TestSearchIndexingGivesUp(t *testing.T) {
	quickRetries(t, &searchRetries)
	index := &fakeSearch{err: errors.New("index down")}
	worker := searchWorker(t, migrated(t), index)

	_, err := enqueueIndexing(t, worker, 7, newIDs(1)).GetResult(dbos.WithHandleTimeout(wait))

	require.ErrorContains(t, err, "index down")
	assert.Len(t, index.state(), 8, "the first attempt and 7 retries")
}

// Search writes are retried 7 more times, waiting 1, 2, 4, 8, then 15
// minutes three times, about an hour, as the docs promise.
func TestSearchRetryPolicy(t *testing.T) {
	assert.Equal(t, retries{attempts: 7, base: time.Minute, ceiling: 15 * time.Minute}, searchRetries)
}

// An account removal removes the account from its member's index.
func TestSearchAccountRemoval(t *testing.T) {
	index := &fakeSearch{}
	worker := searchWorker(t, migrated(t), index)

	handle, err := dbos.Enqueue[struct{}](worker.ctx, searchQueue, searchAccountRemovalWorkflow,
		SearchAccountRemovalInput{OwnerUserID: 7, AccountID: 21},
		dbos.WithEnqueueQueuePartitionKey(searchPartition(7)))
	require.NoError(t, err)
	_, err = handle.GetResult(dbos.WithHandleTimeout(wait))

	require.NoError(t, err)
	assert.Equal(t, []searchCall{{Owner: 7, AccountID: 21}}, index.state())
}

// The search queue runs one of a member's runs at a time even after it was
// changed at runtime: every start restores it.
func TestSearchQueueRestored(t *testing.T) {
	url := migrated(t)
	first := queueWorker(t, url)
	queue, err := dbos.RetrieveQueue(first.ctx, searchQueue)
	require.NoError(t, err)
	require.NoError(t, queue.SetPartitionConcurrency(first.ctx, new(5)))
	require.NoError(t, first.Shutdown())

	second := queueWorker(t, url)
	t.Cleanup(func() { assert.NoError(t, second.Shutdown()) })
	queue, err = dbos.RetrieveQueue(second.ctx, searchQueue)
	require.NoError(t, err)

	assert.Equal(t, new(1), queue.GetPartitionConcurrency())
}

// A sync write and a body save each save a search run in the member's
// partition, which a search worker runs.
func TestClientSearchIndexing(t *testing.T) {
	s := newContentStack(t)
	var member int64
	require.NoError(t, s.conn.QueryRow(t.Context(), "SELECT user_id FROM connected_accounts WHERE id = $1", s.account).Scan(&member))
	var message uuid.UUID
	require.NoError(t, s.conn.QueryRow(t.Context(), "SELECT id FROM messages WHERE connected_account_id = $1", s.account).Scan(&message))

	rows, err := s.conn.Query(t.Context(), "SELECT queue_partition_key FROM dbos.workflow_status WHERE name = $1", searchIndexingWorkflow)
	require.NoError(t, err)
	var partitions []string
	for rows.Next() {
		var partition string
		require.NoError(t, rows.Scan(&partition))
		partitions = append(partitions, partition)
	}
	require.NoError(t, rows.Err())

	index := &fakeSearch{}
	searchWorker(t, s.url, index)
	require.Eventually(t, func() bool { return len(index.state()) == 2 }, wait, 50*time.Millisecond)

	assert.Equal(t, []string{searchPartition(member), searchPartition(member)}, partitions)
	assert.Equal(t, []searchCall{{Owner: member, IDs: []uuid.UUID{message}}, {Owner: member, IDs: []uuid.UUID{message}}}, index.state())
}

// runs counts the saved runs of the workflow name.
func runs(t *testing.T, conn *pgx.Conn, name string) int {
	t.Helper()
	var count int
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM dbos.workflow_status WHERE name = $1", name).Scan(&count))
	return count
}

// An account removal queued behind a member's index run waits until that
// run ends, even while the run fails and retries: a run that read the
// account's messages before the disconnect never writes after the removal.
func TestSearchRemovalWaits(t *testing.T) {
	quickRetries(t, &searchRetries)
	release := make(chan struct{})
	index := &fakeSearch{hold: map[int64]chan struct{}{7: release}, err: errors.New("index down")}
	worker := searchWorker(t, migrated(t), index)

	indexing := enqueueIndexing(t, worker, 7, newIDs(1))
	require.Eventually(t, func() bool { return index.owners()[7] == 1 }, wait, 20*time.Millisecond)
	removal, err := dbos.Enqueue[struct{}](worker.ctx, searchQueue, searchAccountRemovalWorkflow,
		SearchAccountRemovalInput{OwnerUserID: 7, AccountID: 21},
		dbos.WithEnqueueQueuePartitionKey(searchPartition(7)))
	require.NoError(t, err)
	time.Sleep(1500 * time.Millisecond)
	before := index.state()
	close(release)
	_, indexErr := indexing.GetResult(dbos.WithHandleTimeout(wait))
	_, removalErr := removal.GetResult(dbos.WithHandleTimeout(wait))

	require.Error(t, indexErr)
	require.Error(t, removalErr, "the fake fails every call")
	assert.Len(t, before, 1, "the removal did not start while the index run held the partition")
	// Each run makes its first attempt and 7 retries, the index run's all
	// before the removal's first.
	calls := index.state()
	require.Len(t, calls, 16)
	for n, call := range calls {
		if n < 8 {
			assert.Zero(t, call.AccountID, "call %d is the index run's", n)
			continue
		}
		assert.Equal(t, searchCall{Owner: 7, AccountID: 21}, call, "call %d is the removal's", n)
	}
}

// After a restart, a member's unfinished index run resumes in its
// partition: the removal queued behind it still waits for it.
func TestSearchResumeKeepsPartition(t *testing.T) {
	url := migrated(t)

	// Given a worker that stops while a member's index run is in progress,
	// with the member's account removal queued behind it.
	held := &fakeSearch{hold: map[int64]chan struct{}{7: make(chan struct{})}}
	first, err := NewWorker(url, "worker", Services{SearchIndex: held}, discard)
	require.NoError(t, err)
	require.NoError(t, first.Launch())
	indexing := enqueueIndexing(t, first, 7, newIDs(1)).GetWorkflowID()
	require.Eventually(t, func() bool { return held.owners()[7] == 1 }, wait, 20*time.Millisecond)
	removal, err := dbos.Enqueue[struct{}](first.ctx, searchQueue, searchAccountRemovalWorkflow,
		SearchAccountRemovalInput{OwnerUserID: 7, AccountID: 21},
		dbos.WithEnqueueQueuePartitionKey(searchPartition(7)))
	require.NoError(t, err)
	require.NoError(t, first.Shutdown())

	// When the worker starts again with the same ID.
	release := make(chan struct{})
	index := &fakeSearch{hold: map[int64]chan struct{}{7: release}}
	second := searchWorker(t, url, index)
	require.Eventually(t, func() bool { return index.owners()[7] == 1 }, wait, 20*time.Millisecond)
	time.Sleep(1500 * time.Millisecond)
	whileHeld := index.state()
	close(release)
	resumed, err := dbos.RetrieveWorkflow[int](second.ctx, indexing)
	require.NoError(t, err)
	_, indexErr := resumed.GetResult(dbos.WithHandleTimeout(wait))
	removed, err := dbos.RetrieveWorkflow[struct{}](second.ctx, removal.GetWorkflowID())
	require.NoError(t, err)
	_, removalErr := removed.GetResult(dbos.WithHandleTimeout(wait))

	// Then the resumed run goes first, and the removal only after it.
	require.NoError(t, indexErr)
	require.NoError(t, removalErr)
	assert.Len(t, whileHeld, 1, "the removal did not start beside the resumed run")
	assert.Equal(t, searchCall{Owner: 7, AccountID: 21}, index.state()[1])
}
