package dbos

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/connectedaccount"
	"inbok/internal/mailsync"
)

// syncedAccount is the account the tests' full syncs copy.
const syncedAccount = 7

// fakeSyncs runs a full sync of pages listing pages and deleteBatches
// batches of earlier messages, failing a step as its errors say, and
// records each call. A copy of blockPage waits for its context to end and
// reports on pageBlocked; with failBlocked set, so does Fail.
type fakeSyncs struct {
	pages         int
	deleteBatches int
	startErr      error
	copyErr       error
	blockPage     int
	pageBlocked   chan struct{}
	failBlocked   chan struct{}

	mu      sync.Mutex
	calls   []string
	deletes int
	ended   []mailsync.Run
}

// syncRun is the run the fake's Start returns.
var syncRun = mailsync.Run{Generation: 3, Since: time.Date(2026, 8, 12, 9, 30, 0, 0, time.UTC)}

func (f *fakeSyncs) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeSyncs) Start(context.Context, int64) (mailsync.Run, error) {
	f.record("start")
	if f.startErr != nil {
		return mailsync.Run{}, f.startErr
	}
	return syncRun, nil
}

func (f *fakeSyncs) CopyPage(ctx context.Context, _ int64, run mailsync.Run, page int, pageToken string) (string, error) {
	f.record(fmt.Sprintf("copy %d %q", page, pageToken))
	if run != syncRun {
		return "", errors.New("copy of another run")
	}
	if f.pageBlocked != nil && page == f.blockPage {
		f.pageBlocked <- struct{}{}
		<-ctx.Done()
		return "", ctx.Err()
	}
	if f.copyErr != nil {
		return "", f.copyErr
	}
	if page+1 == f.pages {
		return "", nil
	}
	return fmt.Sprintf("page-%d", page+1), nil
}

func (f *fakeSyncs) DeleteEarlier(context.Context, int64, mailsync.Run) (bool, error) {
	f.record("delete earlier")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes++
	return f.deletes < f.deleteBatches, nil
}

func (f *fakeSyncs) Finish(context.Context, int64, mailsync.Run) error {
	f.record("finish")
	return nil
}

func (f *fakeSyncs) Wait(_ context.Context, _ int64, run mailsync.Run) error {
	f.record("wait")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended = append(f.ended, run)
	return nil
}

func (f *fakeSyncs) Fail(ctx context.Context, _ int64, run mailsync.Run, failure mailsync.Failure) error {
	f.record("fail " + string(failure))
	if f.failBlocked != nil {
		f.failBlocked <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended = append(f.ended, run)
	return nil
}

// recorded returns the calls made so far and the runs ended, in order.
func (f *fakeSyncs) recorded() ([]string, []mailsync.Run) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls), slices.Clone(f.ended)
}

// syncWorker launches a worker on url running full syncs with syncs.
func syncWorker(t *testing.T, url string, syncs fullSyncs) *Worker {
	t.Helper()
	worker, err := NewWorker(url, "worker", Services{FullSyncs: syncs}, discard)
	require.NoError(t, err)
	require.NoError(t, worker.Launch())
	return worker
}

// enqueueFullSync enqueues syncedAccount's full sync by name, as a client
// does, and returns its handle.
func enqueueFullSync(t *testing.T, worker *Worker) dbos.WorkflowHandle[mailsync.Status] {
	t.Helper()
	handle, err := dbos.Enqueue[mailsync.Status](worker.ctx, googleQueue, fullSyncWorkflow,
		FullSyncInput{AccountID: syncedAccount},
		dbos.WithEnqueueDeduplicationID(fullSyncDeduplication(syncedAccount)))
	require.NoError(t, err)
	return handle
}

func TestFullSyncWorkflow(t *testing.T) {
	syncs := &fakeSyncs{pages: 3, deleteBatches: 2}
	worker := syncWorker(t, migrated(t), syncs)
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })

	status, err := enqueueFullSync(t, worker).GetResult(dbos.WithHandleTimeout(wait))

	require.NoError(t, err)
	assert.Equal(t, mailsync.StatusComplete, status)
	calls, ended := syncs.recorded()
	assert.Equal(t, []string{
		"start", `copy 0 ""`, `copy 1 "page-1"`, `copy 2 "page-2"`,
		"delete earlier", "delete earlier", "finish",
	}, calls)
	assert.Empty(t, ended)
}

// Each way a step's error ends the full sync records its end once,
// without retrying what retrying cannot mend.
func TestFullSyncWorkflowEnds(t *testing.T) {
	for name, tc := range map[string]struct {
		syncs  *fakeSyncs
		status mailsync.Status
		calls  []string
		ended  []mailsync.Run
	}{
		"needs reconnecting": {
			syncs:  &fakeSyncs{pages: 2, copyErr: connectedaccount.ErrNeedsReconnect},
			status: mailsync.StatusWaiting,
			calls:  []string{"start", `copy 0 ""`, "wait"},
			ended:  []mailsync.Run{syncRun},
		},
		"deactivated before the start": {
			syncs:  &fakeSyncs{startErr: connectedaccount.ErrDeactivated},
			status: mailsync.StatusWaiting,
			calls:  []string{"start", "wait"},
			ended:  []mailsync.Run{{}},
		},
		"unreadable token": {
			syncs:  &fakeSyncs{startErr: fmt.Errorf("%w: no key", connectedaccount.ErrTokenUnreadable)},
			status: mailsync.StatusFailed,
			calls:  []string{"start", "fail token-unreadable"},
			ended:  []mailsync.Run{{}},
		},
		"superseded": {
			syncs:  &fakeSyncs{pages: 2, copyErr: mailsync.ErrSuperseded},
			status: "",
			calls:  []string{"start", `copy 0 ""`},
		},
		"disconnected": {
			syncs:  &fakeSyncs{startErr: connectedaccount.ErrNotFound},
			status: "",
			calls:  []string{"start"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			worker := syncWorker(t, migrated(t), tc.syncs)
			t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })

			status, err := enqueueFullSync(t, worker).GetResult(dbos.WithHandleTimeout(wait))

			// A failed full sync fails its run too, so DBOS keeps it longer.
			if tc.status == mailsync.StatusFailed {
				require.ErrorContains(t, err, "full sync failed")
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.status, status)
			calls, ended := tc.syncs.recorded()
			assert.Equal(t, tc.calls, calls)
			assert.Equal(t, tc.ended, ended)
		})
	}
}

// A step the provider keeps failing is retried with its policy, then the
// full sync fails as the provider's, and the run fails with the error.
func TestFullSyncWorkflowGivesUp(t *testing.T) {
	quickRetries(t, &syncRetries)
	syncs := &fakeSyncs{pages: 2, copyErr: mailsync.ErrProviderBusy}
	worker := syncWorker(t, migrated(t), syncs)
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })

	status, err := enqueueFullSync(t, worker).GetResult(dbos.WithHandleTimeout(wait))

	require.ErrorContains(t, err, "full sync failed: provider")
	assert.Equal(t, mailsync.StatusFailed, status)
	calls, ended := syncs.recorded()
	assert.Equal(t, []string{
		"start", `copy 0 ""`, `copy 0 ""`, `copy 0 ""`, `copy 0 ""`, `copy 0 ""`, "fail provider",
	}, calls, "the first try and 4 retries")
	assert.Equal(t, []mailsync.Run{syncRun}, ended)
}

// A worker stopped during a full sync leaves it pending, and the next one
// with the same ID resumes it at the page it stopped on.
func TestFullSyncWorkflowResume(t *testing.T) {
	// Given a worker stopped while it copies the second page.
	url := migrated(t)
	interrupted := &fakeSyncs{pages: 3, blockPage: 1, pageBlocked: make(chan struct{}, 1)}
	first := syncWorker(t, url, interrupted)
	handle := enqueueFullSync(t, first)
	receive(t, interrupted.pageBlocked, "first worker's second page started")
	require.NoError(t, first.Shutdown())
	assert.Equal(t, "PENDING", workflowStatus(t, url, handle.GetWorkflowID()))

	// When the next worker with the same ID starts.
	resumed := &fakeSyncs{pages: 3, deleteBatches: 1}
	second := syncWorker(t, url, resumed)
	t.Cleanup(func() { assert.NoError(t, second.Shutdown()) })

	// Then it copies from the second page on, with the run the first
	// started, and completes.
	again, err := dbos.RetrieveWorkflow[mailsync.Status](second.ctx, handle.GetWorkflowID())
	require.NoError(t, err)
	status, err := again.GetResult(dbos.WithHandleTimeout(wait))
	require.NoError(t, err)
	assert.Equal(t, mailsync.StatusComplete, status)
	firstCalls, _ := interrupted.recorded()
	secondCalls, _ := resumed.recorded()
	assert.Equal(t, []string{"start", `copy 0 ""`, `copy 1 "page-1"`}, firstCalls)
	assert.Equal(t, []string{`copy 1 "page-1"`, `copy 2 "page-2"`, "delete earlier", "finish"}, secondCalls)
}

// A worker stopped while it records a failure leaves the run pending, and
// the next one records the same failure: how the step failed is saved with
// the step.
func TestFullSyncFailResume(t *testing.T) {
	// Given a worker stopped while it records a page's provider failure.
	quickRetries(t, &syncRetries)
	url := migrated(t)
	interrupted := &fakeSyncs{pages: 2, copyErr: mailsync.ErrProviderBusy, failBlocked: make(chan struct{}, 1)}
	first := syncWorker(t, url, interrupted)
	handle := enqueueFullSync(t, first)
	receive(t, interrupted.failBlocked, "first worker's fail started")
	require.NoError(t, first.Shutdown())

	// When the next worker with the same ID starts.
	resumed := &fakeSyncs{pages: 2}
	second := syncWorker(t, url, resumed)
	t.Cleanup(func() { assert.NoError(t, second.Shutdown()) })

	// Then it records the failure as the provider's, without copying again.
	again, err := dbos.RetrieveWorkflow[mailsync.Status](second.ctx, handle.GetWorkflowID())
	require.NoError(t, err)
	status, err := again.GetResult(dbos.WithHandleTimeout(wait))
	require.ErrorContains(t, err, "full sync failed: provider")
	assert.Equal(t, mailsync.StatusFailed, status)
	calls, _ := resumed.recorded()
	assert.Equal(t, []string{"fail provider"}, calls)
}

// An account has one full sync at a time: a second enqueue while the first
// is queued or running is refused.
func TestFullSyncOnePerAccount(t *testing.T) {
	url := migrated(t)
	syncs := &fakeSyncs{pages: 2, blockPage: 0, pageBlocked: make(chan struct{}, 1)}
	worker := syncWorker(t, url, syncs)
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })
	enqueueFullSync(t, worker)
	receive(t, syncs.pageBlocked, "first full sync started")

	_, err := dbos.Enqueue[mailsync.Status](worker.ctx, googleQueue, fullSyncWorkflow,
		FullSyncInput{AccountID: syncedAccount},
		dbos.WithEnqueueDeduplicationID(fullSyncDeduplication(syncedAccount)))

	require.ErrorIs(t, err, dbos.ErrQueueDeduplicated)
}

// Full sync steps are retried 4 more times, waiting 30 seconds, then 1, 2,
// and 2 minutes, as the backend README says.
func TestSyncRetryPolicy(t *testing.T) {
	assert.Equal(t, retries{attempts: 4, base: 30 * time.Second, ceiling: 2 * time.Minute}, syncRetries)
	waits := make([]time.Duration, syncRetries.attempts)
	for i := range waits {
		waits[i] = syncRetries.wait(i)
	}
	assert.Equal(t, []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 2 * time.Minute}, waits)
}

// A worker stopping while a step waits to retry stops the step at once,
// after one try: the run resumes at the step rather than failing.
func TestSyncRetryStopped(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	tries := 0
	fn := func(context.Context) (string, error) {
		tries++
		if tries == 1 {
			// The worker stops while the step waits its first 30 seconds.
			time.AfterFunc(50*time.Millisecond, cancel)
		}
		return "", mailsync.ErrProviderBusy
	}

	started := time.Now()
	result, err := retrySync(ctx, discard, "copy page", fn)

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, stepResult[string]{}, result)
	assert.Equal(t, 1, tries)
	assert.Less(t, time.Since(started), 5*time.Second)
}
