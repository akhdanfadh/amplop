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

	"amplop/internal/search"
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

// fakeRebuild lists owners and records each member's rebuild, answering
// rebuilt, or the owner's error in errs. A rebuild for an owner in hold
// waits until its channel closes.
type fakeRebuild struct {
	owners  []int64
	rebuilt map[int64]search.Rebuilt
	errs    map[int64]error
	hold    map[int64]chan struct{}

	mu     sync.Mutex
	calls  []int64
	listed int
}

func (f *fakeRebuild) Owners(context.Context) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listed++
	return f.owners, nil
}

func (f *fakeRebuild) Owner(ctx context.Context, owner int64) (search.Rebuilt, error) {
	f.mu.Lock()
	f.calls = append(f.calls, owner)
	f.mu.Unlock()
	if gate, ok := f.hold[owner]; ok {
		select {
		case <-gate:
		case <-ctx.Done():
			return search.Rebuilt{}, ctx.Err()
		}
	}
	return f.rebuilt[owner], f.errs[owner]
}

// rebuilds returns the owners rebuilt so far, in order.
func (f *fakeRebuild) rebuilds() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// rebuildWorker launches a worker on url that indexes with index and
// rebuilds with rebuild.
func rebuildWorker(t *testing.T, url string, index searchIndex, rebuild searchRebuild) *Worker {
	t.Helper()
	worker, err := NewWorker(url, "worker", Services{SearchIndex: index, SearchRebuild: rebuild}, discard)
	require.NoError(t, err)
	require.NoError(t, worker.Launch())
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })
	return worker
}

// newRebuildClient opens a client on url.
func newRebuildClient(t *testing.T, url string) *Client {
	t.Helper()
	client, err := NewClient(t.Context(), url, discard)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	return client
}

// partitions returns the partition of each saved run of the workflow
// name, sorted.
func partitions(t *testing.T, conn *pgx.Conn, name string) []string {
	t.Helper()
	rows, err := conn.Query(t.Context(), "SELECT queue_partition_key FROM dbos.workflow_status WHERE name = $1 ORDER BY queue_partition_key", name)
	require.NoError(t, err)
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	return keys
}

// A rebuild asked for through the client rebuilds each owner in their own
// partition, and returns what the members' rebuilds did, summed.
func TestSearchRebuild(t *testing.T) {
	url := migrated(t)
	rebuild := &fakeRebuild{owners: []int64{7, 8, 9}, rebuilt: map[int64]search.Rebuilt{
		7: {Messages: 3, Leftovers: 1},
		8: {Messages: 2},
		9: {Removed: true},
	}}
	rebuildWorker(t, url, &fakeSearch{}, rebuild)

	output, err := newRebuildClient(t, url).RebuildSearch(t.Context(), wait)

	require.NoError(t, err)
	assert.Equal(t, SearchRebuildOutput{Members: 3, Messages: 5, Leftovers: 1, Removed: 1}, output)
	assert.ElementsMatch(t, []int64{7, 8, 9}, rebuild.rebuilds())
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, conn.Close(context.Background())) })
	assert.Equal(t, []string{"7", "8", "9"}, partitions(t, conn, searchMemberRebuildWorkflow))
}

// A member whose rebuild keeps failing fails the rebuild, naming them, but
// only once the other members' rebuilds have ended.
func TestSearchRebuildFails(t *testing.T) {
	quickRetries(t, &searchRetries)
	url := migrated(t)
	release := make(chan struct{})
	rebuild := &fakeRebuild{
		owners: []int64{7, 8, 9},
		errs:   map[int64]error{8: errors.New("index down")},
		hold:   map[int64]chan struct{}{9: release},
	}
	rebuildWorker(t, url, &fakeSearch{}, rebuild)
	client := newRebuildClient(t, url)

	done := make(chan error, 1)
	go func() {
		_, err := client.RebuildSearch(t.Context(), wait)
		done <- err
	}()
	count := func(owner int64) int {
		n := 0
		for _, o := range rebuild.rebuilds() {
			if o == owner {
				n++
			}
		}
		return n
	}
	require.Eventually(t, func() bool { return count(8) == 8 }, wait, 20*time.Millisecond, "8's first attempt and 7 retries")
	// 9's rebuild is still held, so the rebuild has not ended. DBOS polls a
	// run's result about every second, here twice: the rebuild waiting on
	// 8's, and the client on the rebuild's.
	select {
	case err := <-done:
		require.FailNow(t, "ended before 9's rebuild did", "%v", err)
	case <-time.After(3 * time.Second):
	}
	close(release)
	err := <-done

	require.ErrorContains(t, err, "search rebuild failed for 1 of 3 members, user IDs [8]")
	assert.Equal(t, 1, count(7))
	assert.Equal(t, 1, count(9))
}

// A member's rebuild waits for the member's live index run, in the same
// partition, while another member's goes ahead.
func TestSearchRebuildWaits(t *testing.T) {
	url := migrated(t)
	release := make(chan struct{})
	index := &fakeSearch{hold: map[int64]chan struct{}{7: release}}
	rebuild := &fakeRebuild{owners: []int64{7, 8}}
	worker := rebuildWorker(t, url, index, rebuild)

	indexing := enqueueIndexing(t, worker, 7, newIDs(1))
	require.Eventually(t, func() bool { return index.owners()[7] == 1 }, wait, 20*time.Millisecond)
	client := newRebuildClient(t, url)
	done := make(chan error, 1)
	go func() {
		_, err := client.RebuildSearch(t.Context(), wait)
		done <- err
	}()
	require.Eventually(t, func() bool { return slices.Contains(rebuild.rebuilds(), 8) }, wait, 20*time.Millisecond)
	// DBOS polls queues about every second; 7's rebuild waits instead.
	time.Sleep(1500 * time.Millisecond)
	whileHeld := rebuild.rebuilds()
	close(release)
	_, indexErr := indexing.GetResult(dbos.WithHandleTimeout(wait))

	require.NoError(t, indexErr)
	require.NoError(t, <-done)
	assert.Equal(t, []int64{8}, whileHeld)
	assert.Equal(t, []int64{8, 7}, rebuild.rebuilds())
}

// A client that stops waiting says so, and the rebuild goes on in the
// worker.
func TestSearchRebuildStopsWaiting(t *testing.T) {
	url := migrated(t)
	release := make(chan struct{})
	rebuild := &fakeRebuild{owners: []int64{7}, hold: map[int64]chan struct{}{7: release}}
	worker := rebuildWorker(t, url, &fakeSearch{}, rebuild)

	_, err := newRebuildClient(t, url).RebuildSearch(t.Context(), time.Second)
	close(release)

	require.ErrorContains(t, err, "which goes on in the worker")
	require.Eventually(t, func() bool {
		runs, err := dbos.ListWorkflows(worker.ctx, dbos.WithFilterName(searchRebuildWorkflow), dbos.WithFilterStatus(dbos.WorkflowStatusSuccess))
		require.NoError(t, err)
		return len(runs) == 1
	}, wait, 50*time.Millisecond)
}

// The worker schedules the rebuild nightly at 03:00 UTC, and deletes the
// schedule when it does not rebuild.
func TestSearchRebuildSchedule(t *testing.T) {
	url := migrated(t)
	first, err := NewWorker(url, "worker", Services{SearchIndex: &fakeSearch{}, SearchRebuild: &fakeRebuild{}}, discard)
	require.NoError(t, err)
	schedule, err := dbos.GetSchedule(first.ctx, searchRebuildSchedule)
	require.NoError(t, err)
	assert.Equal(t, "0 0 3 * * *", schedule.Schedule)
	assert.Equal(t, "UTC", schedule.CronTimezone)
	assert.Equal(t, searchRebuildScheduleWorkflow, schedule.WorkflowName)
	require.NoError(t, first.Shutdown())

	second, err := NewWorker(url, "worker", Services{}, discard)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, second.Shutdown()) })
	_, err = dbos.GetSchedule(second.ctx, searchRebuildSchedule)

	require.Error(t, err)
}

// The nightly run rebuilds every member as an asked-for one does.
func TestSearchRebuildNightly(t *testing.T) {
	rebuild := &fakeRebuild{owners: []int64{7, 8}, rebuilt: map[int64]search.Rebuilt{7: {Messages: 1}}}
	worker := rebuildWorker(t, migrated(t), &fakeSearch{}, rebuild)

	handle, err := dbos.TriggerSchedule[SearchRebuildOutput](worker.ctx, searchRebuildSchedule)
	require.NoError(t, err)
	output, err := handle.GetResult(dbos.WithHandleTimeout(wait))

	require.NoError(t, err)
	assert.Equal(t, SearchRebuildOutput{Members: 2, Messages: 1}, output)
	assert.ElementsMatch(t, []int64{7, 8}, rebuild.rebuilds())
}

// A rebuild whose queueing waits for a lock held elsewhere stops at its
// limit, and saves nothing.
func TestClientRebuildCancelled(t *testing.T) {
	url := migrated(t)
	client := newRebuildClient(t, url)
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, conn.Close(context.Background())) })
	lock, err := conn.Begin(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Rollback(context.Background()) })
	_, err = lock.Exec(t.Context(), "LOCK TABLE dbos.workflow_status IN ACCESS EXCLUSIVE MODE")
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, err := client.RebuildSearch(t.Context(), time.Second)
		done <- err
	}()
	var rebuildErr error
	select {
	case rebuildErr = <-done:
	case <-time.After(wait):
		require.FailNow(t, "the rebuild still waits for the lock")
	}
	require.NoError(t, lock.Rollback(t.Context()))

	require.ErrorContains(t, rebuildErr, "stopped queueing the search rebuild")
	assert.Zero(t, runs(t, conn, searchRebuildWorkflow), "nothing was saved")
}

// A worker that stops during a member's rebuild repeats that member whole
// when it starts again, and the rebuild of every member keeps the owners
// it listed and the members' runs it queued.
func TestSearchRebuildResumes(t *testing.T) {
	url := migrated(t)

	// Given a worker that stops while a member's rebuild runs.
	held := &fakeRebuild{owners: []int64{7}, hold: map[int64]chan struct{}{7: make(chan struct{})}}
	first, err := NewWorker(url, "worker", Services{SearchIndex: &fakeSearch{}, SearchRebuild: held}, discard)
	require.NoError(t, err)
	require.NoError(t, first.Launch())
	handle, err := dbos.Enqueue[SearchRebuildOutput](first.ctx, searchRebuildQueue, searchRebuildWorkflow, struct{}{})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return slices.Equal(held.rebuilds(), []int64{7}) }, wait, 20*time.Millisecond)
	require.NoError(t, first.Shutdown())

	// When it starts again with the same ID, with another owner since.
	rebuild := &fakeRebuild{owners: []int64{7, 8}, rebuilt: map[int64]search.Rebuilt{7: {Messages: 4}}}
	second := rebuildWorker(t, url, &fakeSearch{}, rebuild)
	resumed, err := dbos.RetrieveWorkflow[SearchRebuildOutput](second.ctx, handle.GetWorkflowID())
	require.NoError(t, err)
	output, err := resumed.GetResult(dbos.WithHandleTimeout(wait))

	// Then the member is rebuilt again, whole, and only that member.
	require.NoError(t, err)
	assert.Equal(t, SearchRebuildOutput{Members: 1, Messages: 4}, output)
	assert.Equal(t, []int64{7}, rebuild.rebuilds())
	assert.Zero(t, rebuild.listed, "the owners listed before the stop are kept")
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, conn.Close(context.Background())) })
	assert.Equal(t, 1, runs(t, conn, searchMemberRebuildWorkflow))
}
