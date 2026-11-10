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

// syncedAccount is the account the tests' syncs copy.
const syncedAccount = 7

// fakeSyncs runs a full sync of pages listing pages and deleteBatches
// batches of earlier messages,
// failing a step as its errors say, and records each call. A copy of
// blockPage waits for its context to end and reports on pageBlocked; with
// failBlocked set, so does Fail.
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
	if page+1 >= f.pages {
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

// fakePolls plans plan, or fails with planErr, polls pages pages of
// changes of parts parts each, failing a read as readErr says, and records
// each call with its account. A plan with planBlocked set reports on it and
// waits for its context to end.
type fakePolls struct {
	plan        mailsync.Plan
	planErr     error
	pages       int
	parts       int
	readErr     error
	planBlocked chan int64

	mu    sync.Mutex
	calls []string
}

func (f *fakePolls) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakePolls) Plan(ctx context.Context, accountID int64) (mailsync.Plan, error) {
	f.record(fmt.Sprintf("plan %d", accountID))
	if f.planBlocked != nil {
		f.planBlocked <- accountID
		<-ctx.Done()
		return mailsync.Plan{}, ctx.Err()
	}
	return f.plan, f.planErr
}

func (f *fakePolls) ReadChanges(_ context.Context, _ int64, position, pageToken string) (mailsync.ChangePage, error) {
	f.record(fmt.Sprintf("read %s %q", position, pageToken))
	if f.readErr != nil {
		return mailsync.ChangePage{}, f.readErr
	}
	page := 0
	if pageToken != "" {
		if _, err := fmt.Sscanf(pageToken, "changes-%d", &page); err != nil {
			return mailsync.ChangePage{}, err
		}
	}
	read := mailsync.ChangePage{Fetch: []string{fmt.Sprintf("m%d", page)}, Position: "200"}
	if page+1 < f.pages {
		read.NextPageToken = fmt.Sprintf("changes-%d", page+1)
	}
	return read, nil
}

func (f *fakePolls) ApplyChanges(_ context.Context, _ int64, position, pageToken string, page mailsync.ChangePage, part int) (bool, error) {
	f.record(fmt.Sprintf("apply %s %q %v %d", position, pageToken, page.Fetch, part))
	return part+1 < max(f.parts, 1), nil
}

func (f *fakePolls) Fail(_ context.Context, _ int64, position string, failure mailsync.Failure) error {
	f.record(fmt.Sprintf("poll fail %s %s", position, failure))
	return nil
}

func (f *fakePolls) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// fullPlan plans a full sync.
func fullPlan() *fakePolls {
	return &fakePolls{plan: mailsync.Plan{Kind: mailsync.KindFull}}
}

// syncWorker launches a worker on url running syncs with full and polls.
func syncWorker(t *testing.T, url string, full fullSyncs, polls polls) *Worker {
	t.Helper()
	worker, err := NewWorker(url, "worker", Services{FullSyncs: full, Polls: polls}, discard)
	require.NoError(t, err)
	require.NoError(t, worker.Launch())
	return worker
}

// enqueueSync enqueues syncedAccount's sync by name, as a client does, and
// returns its handle.
func enqueueSync(t *testing.T, worker *Worker) dbos.WorkflowHandle[SyncOutput] {
	t.Helper()
	handle, err := dbos.Enqueue[SyncOutput](worker.ctx, googleQueue, syncWorkflow,
		SyncInput{AccountID: syncedAccount},
		dbos.WithEnqueueDeduplicationID(syncDeduplication(syncedAccount)))
	require.NoError(t, err)
	return handle
}

// An account that is not complete gets a full sync: its pages, the sweep,
// and the finish.
func TestSyncFull(t *testing.T) {
	syncs := &fakeSyncs{pages: 3, deleteBatches: 2}
	polls := fullPlan()
	worker := syncWorker(t, migrated(t), syncs, polls)
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })

	output, err := enqueueSync(t, worker).GetResult(dbos.WithHandleTimeout(wait))

	require.NoError(t, err)
	assert.Equal(t, SyncOutput{Kind: mailsync.KindFull, Status: mailsync.StatusComplete}, output)
	calls, ended := syncs.recorded()
	assert.Equal(t, []string{
		"start", `copy 0 ""`, `copy 1 "page-1"`, `copy 2 "page-2"`,
		"delete earlier", "delete earlier", "finish",
	}, calls)
	assert.Empty(t, ended)
	assert.Equal(t, []string{"plan 7"}, polls.recorded())
}

// A complete account gets a poll: each page of changes since its position
// read once and applied part by part with what was read, and no full sync.
func TestSyncPoll(t *testing.T) {
	syncs := &fakeSyncs{}
	polls := &fakePolls{plan: mailsync.Plan{Kind: mailsync.KindPoll, Position: "100"}, pages: 2, parts: 2}
	worker := syncWorker(t, migrated(t), syncs, polls)
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })

	output, err := enqueueSync(t, worker).GetResult(dbos.WithHandleTimeout(wait))

	require.NoError(t, err)
	assert.Equal(t, SyncOutput{Kind: mailsync.KindPoll, Status: mailsync.StatusComplete}, output)
	assert.Equal(t, []string{
		"plan 7",
		`read 100 ""`, `apply 100 "" [m0] 0`, `apply 100 "" [m0] 1`,
		`read 100 "changes-1"`, `apply 100 "changes-1" [m1] 0`, `apply 100 "changes-1" [m1] 1`,
	}, polls.recorded())
	calls, _ := syncs.recorded()
	assert.Empty(t, calls)
}

// A worker stopped during a poll resumes at the part it reached, applying
// the page the first worker read.
func TestSyncPollResume(t *testing.T) {
	// Given a worker stopped while it applies a page's second part.
	url := migrated(t)
	interrupted := &blockingPolls{
		fakePolls: fakePolls{plan: mailsync.Plan{Kind: mailsync.KindPoll, Position: "100"}, pages: 1, parts: 3},
		blockPart: 1, blocked: make(chan struct{}, 1),
	}
	first := syncWorker(t, url, &fakeSyncs{}, interrupted)
	handle := enqueueSync(t, first)
	receive(t, interrupted.blocked, "first worker's second part started")
	require.NoError(t, first.Shutdown())

	// When the next worker with the same ID starts.
	resumed := &fakePolls{plan: mailsync.Plan{Kind: mailsync.KindPoll, Position: "999"}, pages: 1, parts: 3}
	second := syncWorker(t, url, &fakeSyncs{}, resumed)
	t.Cleanup(func() { assert.NoError(t, second.Shutdown()) })

	// Then it applies the second and third parts of the page read before,
	// without planning or reading again.
	again, err := dbos.RetrieveWorkflow[SyncOutput](second.ctx, handle.GetWorkflowID())
	require.NoError(t, err)
	_, err = again.GetResult(dbos.WithHandleTimeout(wait))
	require.NoError(t, err)
	assert.Equal(t, []string{`apply 100 "" [m0] 1`, `apply 100 "" [m0] 2`}, resumed.recorded())
}

// blockingPolls is fakePolls whose apply of blockPart reports on blocked
// and waits for its context to end.
type blockingPolls struct {
	fakePolls
	blockPart int
	blocked   chan struct{}
}

func (b *blockingPolls) ApplyChanges(ctx context.Context, accountID int64, position, pageToken string, page mailsync.ChangePage, part int) (bool, error) {
	if part == b.blockPart {
		b.record("blocked")
		b.blocked <- struct{}{}
		<-ctx.Done()
		return false, ctx.Err()
	}
	return b.fakePolls.ApplyChanges(ctx, accountID, position, pageToken, page, part)
}

// A poll whose changes the provider no longer keeps goes on to a full
// sync in the same run.
func TestSyncPollResync(t *testing.T) {
	syncs := &fakeSyncs{pages: 1, deleteBatches: 1}
	polls := &fakePolls{
		plan:    mailsync.Plan{Kind: mailsync.KindPoll, Position: "100"},
		readErr: fmt.Errorf("%w: gmail api: 404", mailsync.ErrPositionExpired),
	}
	worker := syncWorker(t, migrated(t), syncs, polls)
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })

	output, err := enqueueSync(t, worker).GetResult(dbos.WithHandleTimeout(wait))

	require.NoError(t, err)
	assert.Equal(t, SyncOutput{Kind: mailsync.KindFull, Status: mailsync.StatusComplete}, output)
	assert.Equal(t, []string{"plan 7", `read 100 ""`}, polls.recorded(), "not retried")
	calls, _ := syncs.recorded()
	assert.Equal(t, []string{"start", `copy 0 ""`, "delete earlier", "finish"}, calls)
}

// Each way a poll's step ends it records its end once, and leaves the
// account complete.
func TestSyncPollEnds(t *testing.T) {
	for name, tc := range map[string]struct {
		readErr error
		output  SyncOutput
		failed  bool
		calls   []string
	}{
		"unreadable token": {
			readErr: fmt.Errorf("%w: no key", connectedaccount.ErrTokenUnreadable),
			output:  SyncOutput{Kind: mailsync.KindPoll, Status: mailsync.StatusComplete},
			failed:  true,
			calls:   []string{"plan 7", `read 100 ""`, "poll fail 100 token-unreadable"},
		},
		"needs reconnecting": {
			readErr: connectedaccount.ErrNeedsReconnect,
			output:  SyncOutput{Kind: mailsync.KindPoll, Status: mailsync.StatusComplete},
			calls:   []string{"plan 7", `read 100 ""`},
		},
		"superseded": {
			readErr: mailsync.ErrSuperseded,
			output:  SyncOutput{Kind: mailsync.KindPoll},
			calls:   []string{"plan 7", `read 100 ""`},
		},
	} {
		t.Run(name, func(t *testing.T) {
			polls := &fakePolls{plan: mailsync.Plan{Kind: mailsync.KindPoll, Position: "100"}, readErr: tc.readErr}
			worker := syncWorker(t, migrated(t), &fakeSyncs{}, polls)
			t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })

			output, err := enqueueSync(t, worker).GetResult(dbos.WithHandleTimeout(wait))

			if tc.failed {
				require.ErrorContains(t, err, "sync failed: poll: token-unreadable")
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.output, output)
			assert.Equal(t, tc.calls, polls.recorded())
		})
	}
}

// A poll the provider keeps failing is retried with its policy, then
// records the provider's failure.
func TestSyncPollGivesUp(t *testing.T) {
	quickRetries(t, &syncRetries)
	polls := &fakePolls{plan: mailsync.Plan{Kind: mailsync.KindPoll, Position: "100"}, readErr: mailsync.ErrProviderBusy}
	worker := syncWorker(t, migrated(t), &fakeSyncs{}, polls)
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })

	_, err := enqueueSync(t, worker).GetResult(dbos.WithHandleTimeout(wait))

	require.ErrorContains(t, err, "sync failed: poll: provider")
	assert.Equal(t, []string{
		"plan 7", `read 100 ""`, `read 100 ""`, `read 100 ""`, `read 100 ""`, `read 100 ""`, "poll fail 100 provider",
	}, polls.recorded())
}

// An account gone before its run chose records nothing.
func TestSyncPlanGone(t *testing.T) {
	syncs := &fakeSyncs{}
	polls := &fakePolls{planErr: mailsync.ErrNotFound}
	worker := syncWorker(t, migrated(t), syncs, polls)
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })

	output, err := enqueueSync(t, worker).GetResult(dbos.WithHandleTimeout(wait))

	require.NoError(t, err)
	assert.Equal(t, SyncOutput{}, output)
	calls, _ := syncs.recorded()
	assert.Empty(t, calls)
}

// Each way a step's error ends the full sync records its end once,
// without retrying what retrying cannot mend.
func TestSyncFullEnds(t *testing.T) {
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
			worker := syncWorker(t, migrated(t), tc.syncs, fullPlan())
			t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })

			output, err := enqueueSync(t, worker).GetResult(dbos.WithHandleTimeout(wait))

			// A failed full sync fails its run too, so DBOS keeps it longer.
			if tc.status == mailsync.StatusFailed {
				require.ErrorContains(t, err, "sync failed: full")
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, SyncOutput{Kind: mailsync.KindFull, Status: tc.status}, output)
			calls, ended := tc.syncs.recorded()
			assert.Equal(t, tc.calls, calls)
			assert.Equal(t, tc.ended, ended)
		})
	}
}

// A step the provider keeps failing is retried with its policy, then the
// full sync fails as the provider's, and the run fails with the error.
func TestSyncFullGivesUp(t *testing.T) {
	quickRetries(t, &syncRetries)
	syncs := &fakeSyncs{pages: 2, copyErr: mailsync.ErrProviderBusy}
	worker := syncWorker(t, migrated(t), syncs, fullPlan())
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })

	output, err := enqueueSync(t, worker).GetResult(dbos.WithHandleTimeout(wait))

	require.ErrorContains(t, err, "sync failed: full: provider")
	assert.Equal(t, mailsync.StatusFailed, output.Status)
	calls, ended := syncs.recorded()
	assert.Equal(t, []string{
		"start", `copy 0 ""`, `copy 0 ""`, `copy 0 ""`, `copy 0 ""`, `copy 0 ""`, "fail provider",
	}, calls, "the first try and 4 retries")
	assert.Equal(t, []mailsync.Run{syncRun}, ended)
}

// A worker stopped during a full sync leaves it pending, and the next one
// with the same ID resumes it at the page it stopped on.
func TestSyncFullResume(t *testing.T) {
	// Given a worker stopped while it copies the second page.
	url := migrated(t)
	interrupted := &fakeSyncs{pages: 3, blockPage: 1, pageBlocked: make(chan struct{}, 1)}
	first := syncWorker(t, url, interrupted, fullPlan())
	handle := enqueueSync(t, first)
	receive(t, interrupted.pageBlocked, "first worker's second page started")
	require.NoError(t, first.Shutdown())
	assert.Equal(t, "PENDING", workflowStatus(t, url, handle.GetWorkflowID()))

	// When the next worker with the same ID starts.
	resumed := &fakeSyncs{pages: 3, deleteBatches: 1}
	resumedPolls := fullPlan()
	second := syncWorker(t, url, resumed, resumedPolls)
	t.Cleanup(func() { assert.NoError(t, second.Shutdown()) })

	// Then it copies from the second page on, with the run the first
	// started and the plan it chose, and completes.
	again, err := dbos.RetrieveWorkflow[SyncOutput](second.ctx, handle.GetWorkflowID())
	require.NoError(t, err)
	output, err := again.GetResult(dbos.WithHandleTimeout(wait))
	require.NoError(t, err)
	assert.Equal(t, mailsync.StatusComplete, output.Status)
	firstCalls, _ := interrupted.recorded()
	secondCalls, _ := resumed.recorded()
	assert.Equal(t, []string{"start", `copy 0 ""`, `copy 1 "page-1"`}, firstCalls)
	assert.Equal(t, []string{`copy 1 "page-1"`, `copy 2 "page-2"`, "delete earlier", "finish"}, secondCalls)
	assert.Empty(t, resumedPolls.recorded(), "the plan is not made again")
}

// A worker stopped while it records a failure leaves the run pending, and
// the next one records the same failure: how the step failed is saved with
// the step.
func TestSyncFailResume(t *testing.T) {
	// Given a worker stopped while it records a page's provider failure.
	quickRetries(t, &syncRetries)
	url := migrated(t)
	interrupted := &fakeSyncs{pages: 2, copyErr: mailsync.ErrProviderBusy, failBlocked: make(chan struct{}, 1)}
	first := syncWorker(t, url, interrupted, fullPlan())
	handle := enqueueSync(t, first)
	receive(t, interrupted.failBlocked, "first worker's fail started")
	require.NoError(t, first.Shutdown())

	// When the next worker with the same ID starts.
	resumed := &fakeSyncs{pages: 2}
	second := syncWorker(t, url, resumed, fullPlan())
	t.Cleanup(func() { assert.NoError(t, second.Shutdown()) })

	// Then it records the failure as the provider's, without copying again.
	again, err := dbos.RetrieveWorkflow[SyncOutput](second.ctx, handle.GetWorkflowID())
	require.NoError(t, err)
	output, err := again.GetResult(dbos.WithHandleTimeout(wait))
	require.ErrorContains(t, err, "sync failed: full: provider")
	assert.Equal(t, mailsync.StatusFailed, output.Status)
	calls, _ := resumed.recorded()
	assert.Equal(t, []string{"fail provider"}, calls)
}

// An account has one sync, poll or full, at a time: a second enqueue while
// the first is queued or running is refused.
func TestSyncOnePerAccount(t *testing.T) {
	url := migrated(t)
	syncs := &fakeSyncs{pages: 2, blockPage: 0, pageBlocked: make(chan struct{}, 1)}
	worker := syncWorker(t, url, syncs, fullPlan())
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })
	enqueueSync(t, worker)
	receive(t, syncs.pageBlocked, "first sync started")

	_, err := dbos.Enqueue[SyncOutput](worker.ctx, googleQueue, syncWorkflow,
		SyncInput{AccountID: syncedAccount},
		dbos.WithEnqueueDeduplicationID(syncDeduplication(syncedAccount)))

	require.ErrorIs(t, err, dbos.ErrQueueDeduplicated)
}

// fakeDue answers every pick with accounts.
type fakeDue struct {
	accounts []int64
}

func (f fakeDue) Due(context.Context) ([]int64, error) {
	return f.accounts, nil
}

// scheduleWorker launches a worker on url that schedules syncs for due and
// runs them with polls, with the schedule paused so only the test triggers
// it, and shuts it down when the test ends.
func scheduleWorker(t *testing.T, url string, due dueSyncs, polls polls) *Worker {
	t.Helper()
	worker, err := NewWorker(url, "worker", Services{FullSyncs: &fakeSyncs{}, Polls: polls, SyncSchedule: due}, discard)
	require.NoError(t, err)
	// A run firing on the minute during a test would race with the trigger.
	require.NoError(t, dbos.PauseSchedule(worker.ctx, syncSchedule))
	require.NoError(t, worker.Launch())
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })
	return worker
}

// The worker schedules syncs every minute, in UTC.
func TestSyncSchedule(t *testing.T) {
	worker := scheduleWorker(t, migrated(t), fakeDue{}, fullPlan())

	schedule, err := dbos.GetSchedule(worker.ctx, syncSchedule)
	require.NoError(t, err)

	assert.Equal(t, "0 * * * * *", schedule.Schedule)
	assert.Equal(t, "UTC", schedule.CronTimezone)
	assert.Equal(t, syncScheduleWorkflow, schedule.WorkflowName)
}

// A run starts a sync for each account due, and a run while those still
// run starts no second one for an account.
func TestSyncScheduleStarts(t *testing.T) {
	url := migrated(t)
	polls := &fakePolls{planBlocked: make(chan int64, 4)}
	worker := scheduleWorker(t, url, fakeDue{accounts: []int64{7, 8}}, polls)

	for range 2 {
		handle, err := dbos.TriggerSchedule[int](worker.ctx, syncSchedule)
		require.NoError(t, err)
		started, err := handle.GetResult(dbos.WithHandleTimeout(wait))
		require.NoError(t, err)
		assert.Equal(t, 2, started)
	}

	runs, err := dbos.ListWorkflows(worker.ctx, dbos.WithFilterName(syncWorkflow))
	require.NoError(t, err)
	var deduplication []string
	for _, run := range runs {
		deduplication = append(deduplication, run.DeduplicationID)
	}
	slices.Sort(deduplication)
	assert.Equal(t, []string{"sync-7", "sync-8"}, deduplication)
}

// A worker without Google deletes the schedule an earlier start saved, so
// no run fires for a workflow it lacks.
func TestSyncScheduleDeleted(t *testing.T) {
	url := migrated(t)
	first, err := NewWorker(url, "worker", Services{FullSyncs: &fakeSyncs{}, Polls: fullPlan(), SyncSchedule: fakeDue{}}, discard)
	require.NoError(t, err)
	require.NoError(t, first.Shutdown())

	second, err := NewWorker(url, "worker", Services{}, discard)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, second.Shutdown()) })

	_, err = dbos.GetSchedule(second.ctx, syncSchedule)
	require.Error(t, err)
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
