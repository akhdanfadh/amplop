package dbos

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/connectedaccount"
	"inbok/internal/messagecontent"
)

// fakeBodies answers [fakeBodies.DueAccounts] with accounts, and each
// [fakeBodies.FetchDue] with the next of batches, or err once they run
// out; with more set, every batch says more may be due. With hold set, a
// fetch first waits for it to close, and with pickHold, a pick does.
type fakeBodies struct {
	accounts []int64
	batches  []messagecontent.Batch
	err      error
	more     bool
	hold     chan struct{}
	pickHold chan struct{}

	mu      sync.Mutex
	checks  int
	fetches []int64
}

func (f *fakeBodies) DueAccounts(ctx context.Context) ([]int64, error) {
	if f.pickHold != nil {
		select {
		case <-f.pickHold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks++
	return f.accounts, nil
}

func (f *fakeBodies) FetchDue(ctx context.Context, accountID int64) (messagecontent.Batch, error) {
	if f.hold != nil {
		select {
		case <-f.hold:
		case <-ctx.Done():
			return messagecontent.Batch{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetches = append(f.fetches, accountID)
	if f.more {
		return messagecontent.Batch{Saved: 1, More: true}, nil
	}
	if len(f.batches) == 0 {
		return messagecontent.Batch{}, f.err
	}
	batch := f.batches[0]
	f.batches = f.batches[1:]
	return batch, nil
}

// recorded returns how many checks came, and the accounts fetched for,
// in order.
func (f *fakeBodies) recorded() (checks int, fetches []int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checks, append([]int64(nil), f.fetches...)
}

// bodiesWorker launches a worker on url fetching bodies with bodies, its
// sync schedule picking syncs with due; both schedules are paused, so only
// the test triggers them.
func bodiesWorker(t *testing.T, url string, bodies bodyFetches, due dueSyncs) *Worker {
	t.Helper()
	worker, err := NewWorker(url, "worker", Services{FullSyncs: &fakeSyncs{}, Polls: fullPlan(), SyncSchedule: due, MessageBodies: bodies}, discard)
	require.NoError(t, err)
	// A run firing on the minute during a test would race with the trigger.
	require.NoError(t, dbos.PauseSchedule(worker.ctx, syncSchedule))
	require.NoError(t, dbos.PauseSchedule(worker.ctx, messageBodiesSchedule))
	require.NoError(t, worker.Launch())
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })
	return worker
}

// runBodies enqueues syncedAccount's body fetch and returns its result.
func runBodies(t *testing.T, worker *Worker) (MessageBodiesOutput, error) {
	t.Helper()
	handle, err := dbos.Enqueue[MessageBodiesOutput](worker.ctx, googleQueue, messageBodiesWorkflow, MessageBodiesInput{AccountID: syncedAccount})
	require.NoError(t, err)
	return handle.GetResult(dbos.WithHandleTimeout(wait))
}

// A run takes batches until none says more may be due.
func TestMessageBodies(t *testing.T) {
	bodies := &fakeBodies{err: errors.New("asked once too often"), batches: []messagecontent.Batch{
		{Saved: 18, Failed: 2, More: true}, {Saved: 5},
	}}
	worker := bodiesWorker(t, migrated(t), bodies, fakeDue{})

	output, err := runBodies(t, worker)

	require.NoError(t, err)
	assert.Equal(t, MessageBodiesOutput{Saved: 23, Failed: 2}, output)
	_, fetches := bodies.recorded()
	assert.Equal(t, []int64{syncedAccount, syncedAccount}, fetches)
}

// An account whose provider work stopped ends the run quietly; the sync
// records why.
func TestMessageBodiesStopped(t *testing.T) {
	for _, stop := range []error{connectedaccount.ErrNeedsReconnect, connectedaccount.ErrNotFound, connectedaccount.ErrTokenUnreadable} {
		t.Run(stop.Error(), func(t *testing.T) {
			bodies := &fakeBodies{err: stop}
			worker := bodiesWorker(t, migrated(t), bodies, fakeDue{})

			output, err := runBodies(t, worker)

			require.NoError(t, err)
			assert.Equal(t, MessageBodiesOutput{}, output)
			_, fetches := bodies.recorded()
			assert.Len(t, fetches, 1)
		})
	}
}

// Any other failure fails the run at once, for the next minute's schedule
// to start again.
func TestMessageBodiesFails(t *testing.T) {
	bodies := &fakeBodies{err: errors.New("provider down"), batches: []messagecontent.Batch{{Saved: 20, More: true}}}
	worker := bodiesWorker(t, migrated(t), bodies, fakeDue{})

	_, err := runBodies(t, worker)

	require.ErrorContains(t, err, "provider down")
	_, fetches := bodies.recorded()
	assert.Len(t, fetches, 2)
}

// A run ends after its batch limit, however many are due.
func TestMessageBodiesLimit(t *testing.T) {
	bodies := &fakeBodies{more: true}
	worker := bodiesWorker(t, migrated(t), bodies, fakeDue{})

	output, err := runBodies(t, worker)

	require.NoError(t, err)
	assert.Equal(t, MessageBodiesOutput{Saved: maxBodyBatches}, output)
}

// triggerBodies runs the minute's body schedule once and returns how many
// body fetches it asked for.
func triggerBodies(t *testing.T, worker *Worker) int {
	t.Helper()
	handle, err := dbos.TriggerSchedule[int](worker.ctx, messageBodiesSchedule)
	require.NoError(t, err)
	started, err := handle.GetResult(dbos.WithHandleTimeout(wait))
	require.NoError(t, err)
	return started
}

// bodyRuns returns the deduplication IDs of the body fetch runs.
func bodyRuns(t *testing.T, worker *Worker) []string {
	t.Helper()
	runs, err := dbos.ListWorkflows(worker.ctx, dbos.WithFilterName(messageBodiesWorkflow))
	require.NoError(t, err)
	var ids []string
	for _, run := range runs {
		ids = append(ids, run.DeduplicationID)
	}
	return ids
}

// The worker schedules its body pick every minute, in UTC, apart from the
// sync schedule; a worker without Google deletes the schedule.
func TestMessageBodiesSchedule(t *testing.T) {
	url := migrated(t)
	first, err := NewWorker(url, "worker", Services{MessageBodies: &fakeBodies{}}, discard)
	require.NoError(t, err)
	schedule, err := dbos.GetSchedule(first.ctx, messageBodiesSchedule)
	require.NoError(t, err)
	assert.Equal(t, "0 * * * * *", schedule.Schedule)
	assert.Equal(t, "UTC", schedule.CronTimezone)
	assert.Equal(t, messageBodiesScheduleWorkflow, schedule.WorkflowName)
	require.NoError(t, first.Shutdown())

	second, err := NewWorker(url, "worker", Services{}, discard)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, second.Shutdown()) })
	_, err = dbos.GetSchedule(second.ctx, messageBodiesSchedule)
	require.Error(t, err)
}

// The body schedule starts a body fetch for each account whose bodies are
// due, a quiet one the sync schedule starts nothing for too, so a run that
// ended at its limit or failed is started again the next minute.
func TestMessageBodiesScheduleStarts(t *testing.T) {
	bodies := &fakeBodies{accounts: []int64{syncedAccount}, err: errors.New("provider down"), batches: []messagecontent.Batch{{Saved: 20, More: true}}}
	worker := bodiesWorker(t, migrated(t), bodies, fakeDue{})

	// The first run takes a batch, then fails on the next.
	assert.Equal(t, 1, triggerBodies(t, worker))
	require.Eventually(t, func() bool { _, fetches := bodies.recorded(); return len(fetches) == 2 }, wait, 50*time.Millisecond)
	require.Eventually(t, func() bool {
		runs, err := dbos.ListWorkflows(worker.ctx, dbos.WithFilterName(messageBodiesWorkflow), dbos.WithFilterStatus(dbos.WorkflowStatusError))
		require.NoError(t, err)
		return len(runs) == 1
	}, wait, 50*time.Millisecond)

	// The next minute starts another.
	triggerBodies(t, worker)
	require.Eventually(t, func() bool { _, fetches := bodies.recorded(); return len(fetches) == 3 }, wait, 50*time.Millisecond)
	checks, fetches := bodies.recorded()
	assert.Equal(t, 2, checks)
	assert.Equal(t, []int64{syncedAccount, syncedAccount, syncedAccount}, fetches)
	// DBOS clears a finished run's deduplication ID, so only the count shows.
	assert.Len(t, bodyRuns(t, worker), 2)
}

// A minute that finds an account's body fetch still queued or running
// starts no second one.
func TestMessageBodiesScheduleOnce(t *testing.T) {
	bodies := &fakeBodies{accounts: []int64{syncedAccount, syncedAccount + 1}, hold: make(chan struct{})}
	worker := bodiesWorker(t, migrated(t), bodies, fakeDue{})
	// Registered after the worker's shutdown, so it runs first.
	t.Cleanup(func() { close(bodies.hold) })

	triggerBodies(t, worker)
	triggerBodies(t, worker)

	ids := bodyRuns(t, worker)
	slices.Sort(ids)
	assert.Equal(t, []string{"message-bodies-7", "message-bodies-8"}, ids)
}

// A body pick that never returns, e.g. behind a lock, leaves the sync
// schedule's runs, and so syncing's health, as they were.
func TestMessageBodiesScheduleApart(t *testing.T) {
	url := migrated(t)
	bodies := &fakeBodies{accounts: []int64{syncedAccount}, pickHold: make(chan struct{})}
	worker := bodiesWorker(t, url, bodies, fakeDue{})
	t.Cleanup(func() { close(bodies.pickHold) })
	client, err := NewClient(t.Context(), url, discard)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	before := time.Now().Add(-time.Minute)

	_, err = dbos.TriggerSchedule[int](worker.ctx, messageBodiesSchedule)
	require.NoError(t, err)
	handle, err := dbos.TriggerSchedule[int](worker.ctx, syncSchedule)
	require.NoError(t, err)
	_, err = handle.GetResult(dbos.WithHandleTimeout(wait))
	require.NoError(t, err)

	succeeded, err := client.SyncScheduleSucceeded(t.Context(), before)
	require.NoError(t, err)
	assert.True(t, succeeded)
	checks, _ := bodies.recorded()
	assert.Zero(t, checks, "the pick is still held")
}
