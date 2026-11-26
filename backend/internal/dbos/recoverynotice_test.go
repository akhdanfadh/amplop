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

	"amplop/internal/account"
)

// recoveredAt is when [recoveredID] was recovered in these tests.
var recoveredAt = time.Date(2026, 11, 3, 11, 16, 0, 0, time.UTC)

// IDs of the administrators in these tests: [recoveredID] was recovered.
const (
	recoveredID int64 = 7
	firstAdmin  int64 = 3
	lastAdmin   int64 = 9
)

// fakeNotices lists admins, or fails with listErr, and answers each recipient's sends with their
// results, one per call, repeating the last; a recipient with none is sent
// to. A send to blockOn waits for its
// context to end and reports on sendBlocked; with blockList set, so does
// the list, on listBlocked. It records its calls and the recovery each
// send was about.
type fakeNotices struct {
	admins    []int64
	listErr   error
	blockList bool
	results   map[int64][]sendResult
	blockOn   int64

	mu          sync.Mutex
	lists       int
	sends       []int64
	recoveries  []RecoveryNoticeInput
	sendBlocked chan struct{}
	listBlocked chan struct{}
}

type sendResult struct {
	outcome account.NoticeOutcome
	err     error
}

func (f *fakeNotices) Administrators(ctx context.Context) ([]int64, error) {
	f.mu.Lock()
	f.lists++
	f.mu.Unlock()
	if f.blockList {
		f.listBlocked <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return slices.Clone(f.admins), f.listErr
}

func (f *fakeNotices) Send(ctx context.Context, recovered int64, at time.Time, recipientID int64) (account.NoticeOutcome, error) {
	f.mu.Lock()
	f.sends = append(f.sends, recipientID)
	f.recoveries = append(f.recoveries, RecoveryNoticeInput{UserID: recovered, RecoveredAt: at})
	calls := 0
	for _, id := range f.sends {
		if id == recipientID {
			calls++
		}
	}
	results := f.results[recipientID]
	f.mu.Unlock()

	if recipientID == f.blockOn {
		f.sendBlocked <- struct{}{}
		<-ctx.Done()
		return "", ctx.Err()
	}
	if len(results) == 0 {
		return account.NoticeSent, nil
	}
	result := results[min(calls, len(results))-1]
	return result.outcome, result.err
}

// recovered returns the recovery each Send was about, in order.
func (f *fakeNotices) recovered() []RecoveryNoticeInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.recoveries)
}

// calls returns how often Administrators ran and whom Send was called for,
// in order.
func (f *fakeNotices) calls() (int, []int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lists, slices.Clone(f.sends)
}

// The list is retried 10 more times within about 5 minutes; the docs
// promise it. The send uses [noticeRetries].
func TestRecoveryNoticeRetryPolicy(t *testing.T) {
	assert.Equal(t, retries{attempts: 10, base: time.Second, ceiling: time.Minute}, listRetries)
}

// noticeWorker launches a worker on url sending notices with notices.
func noticeWorker(t *testing.T, url string, notices recoveryNotices) *Worker {
	t.Helper()

	worker, err := NewWorker(url, "worker", Services{RecoveryNotices: notices}, discard)
	require.NoError(t, err)
	require.NoError(t, worker.Launch())
	return worker
}

// enqueueNotice enqueues the notice of [recoveredID]'s recovery at
// [recoveredAt] by name, as a client does, and returns its handle.
func enqueueNotice(t *testing.T, worker *Worker) dbos.WorkflowHandle[[]recipientOutcome] {
	t.Helper()

	handle, err := dbos.Enqueue[[]recipientOutcome](worker.ctx, emailQueue, recoveryNoticeWorkflow, RecoveryNoticeInput{UserID: recoveredID, RecoveredAt: recoveredAt})
	require.NoError(t, err)
	return handle
}

// The notice goes to each administrator listed, the recovered one first,
// and the run records how each ended.
func TestRecoveryNoticeSent(t *testing.T) {
	notices := &fakeNotices{
		admins:  []int64{firstAdmin, recoveredID, lastAdmin},
		results: map[int64][]sendResult{lastAdmin: {{outcome: account.NoticeNoRecipient}}},
	}
	worker := noticeWorker(t, migrated(t), notices)
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })

	outcomes, err := enqueueNotice(t, worker).GetResult(dbos.WithHandleTimeout(wait))
	require.NoError(t, err)

	assert.Equal(t, []recipientOutcome{
		{UserID: recoveredID, Outcome: account.NoticeSent},
		{UserID: firstAdmin, Outcome: account.NoticeSent},
		{UserID: lastAdmin, Outcome: account.NoticeNoRecipient},
	}, outcomes)
	lists, sends := notices.calls()
	assert.Equal(t, 1, lists)
	assert.Equal(t, []int64{recoveredID, firstAdmin, lastAdmin}, sends)
	// The input survives DBOS's JSON, time included.
	want := RecoveryNoticeInput{UserID: recoveredID, RecoveredAt: recoveredAt}
	for _, got := range notices.recovered() {
		assert.Equal(t, want.UserID, got.UserID)
		assert.WithinDuration(t, want.RecoveredAt, got.RecoveredAt, 0)
	}
}

// A send that fails is retried, the same step running again, until it
// passes.
func TestRecoveryNoticeRetries(t *testing.T) {
	quickRetries(t, &listRetries, &noticeRetries)
	failed := sendResult{err: errors.New("connection refused")}
	notices := &fakeNotices{
		admins:  []int64{recoveredID},
		results: map[int64][]sendResult{recoveredID: {failed, failed, {outcome: account.NoticeSent}}},
	}
	worker := noticeWorker(t, migrated(t), notices)
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })

	outcomes, err := enqueueNotice(t, worker).GetResult(dbos.WithHandleTimeout(wait))
	require.NoError(t, err)

	assert.Equal(t, []recipientOutcome{{UserID: recoveredID, Outcome: account.NoticeSent}}, outcomes)
	_, sends := notices.calls()
	assert.Equal(t, []int64{recoveredID, recoveredID, recoveredID}, sends)
}

// A send that keeps failing gives up after its retries; the others still
// go out, and the run fails with its error.
func TestRecoveryNoticeGivesUp(t *testing.T) {
	quickRetries(t, &listRetries, &noticeRetries)
	notices := &fakeNotices{
		admins:  []int64{recoveredID, lastAdmin},
		results: map[int64][]sendResult{recoveredID: {{err: errors.New("connection refused")}}},
	}
	worker := noticeWorker(t, migrated(t), notices)
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })

	_, err := enqueueNotice(t, worker).GetResult(dbos.WithHandleTimeout(wait))

	require.ErrorContains(t, err, "connection refused")
	_, sends := notices.calls()
	want := append(slices.Repeat([]int64{recoveredID}, 6), lastAdmin)
	assert.Equal(t, want, sends, "the first attempt and 5 retries, then the next")
}

// A worker stopped during a send leaves the notice pending; the next
// worker resumes it at that send, without listing again or repeating the
// sends that finished.
func TestRecoveryNoticeResume(t *testing.T) {
	// Given a worker stopped while its second send waits, its list put in
	// order by the workflow.
	url := migrated(t)
	admins := []int64{firstAdmin, recoveredID, lastAdmin}
	interrupted := &fakeNotices{admins: admins, blockOn: firstAdmin, sendBlocked: make(chan struct{}, 1)}
	first := noticeWorker(t, url, interrupted)
	handle := enqueueNotice(t, first)
	receive(t, interrupted.sendBlocked, "first worker's second send started")
	require.NoError(t, first.Shutdown())
	assert.Equal(t, "PENDING", workflowStatus(t, url, handle.GetWorkflowID()))

	// When the next worker with the same ID starts, its sends able to pass.
	resumed := &fakeNotices{admins: admins}
	second := noticeWorker(t, url, resumed)
	t.Cleanup(func() { assert.NoError(t, second.Shutdown()) })

	// Then the notice reaches everyone, the list made and the first send
	// done only by the first worker.
	again, err := dbos.RetrieveWorkflow[[]recipientOutcome](second.ctx, handle.GetWorkflowID())
	require.NoError(t, err)
	outcomes, err := again.GetResult(dbos.WithHandleTimeout(wait))
	require.NoError(t, err)
	assert.Equal(t, []recipientOutcome{
		{UserID: recoveredID, Outcome: account.NoticeSent},
		{UserID: firstAdmin, Outcome: account.NoticeSent},
		{UserID: lastAdmin, Outcome: account.NoticeSent},
	}, outcomes)
	firstLists, firstSends := interrupted.calls()
	secondLists, secondSends := resumed.calls()
	assert.Equal(t, []int{1, 0}, []int{firstLists, secondLists})
	assert.Equal(t, []int64{recoveredID, firstAdmin}, firstSends)
	assert.Equal(t, []int64{firstAdmin, lastAdmin}, secondSends)
}

// With no active administrator, the run sends nothing and succeeds.
func TestRecoveryNoticeNoAdministrators(t *testing.T) {
	notices := &fakeNotices{}
	worker := noticeWorker(t, migrated(t), notices)
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })

	outcomes, err := enqueueNotice(t, worker).GetResult(dbos.WithHandleTimeout(wait))
	require.NoError(t, err)

	assert.Empty(t, outcomes)
	_, sends := notices.calls()
	assert.Empty(t, sends)
}

// A list that keeps failing gives up after its retries; the run fails and
// sends nothing.
func TestRecoveryNoticeListGivesUp(t *testing.T) {
	quickRetries(t, &listRetries, &noticeRetries)
	notices := &fakeNotices{admins: []int64{recoveredID}, listErr: errors.New("connection refused")}
	worker := noticeWorker(t, migrated(t), notices)
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })

	_, err := enqueueNotice(t, worker).GetResult(dbos.WithHandleTimeout(wait))

	require.ErrorContains(t, err, "connection refused")
	lists, sends := notices.calls()
	assert.Equal(t, 11, lists, "the first attempt and 10 retries")
	assert.Empty(t, sends)
}

// A worker stopped while listing leaves the notice pending; the next worker
// lists and sends.
func TestRecoveryNoticeResumeList(t *testing.T) {
	// Given a worker stopped while its list waits.
	url := migrated(t)
	interrupted := &fakeNotices{blockList: true, listBlocked: make(chan struct{}, 1)}
	first := noticeWorker(t, url, interrupted)
	handle := enqueueNotice(t, first)
	receive(t, interrupted.listBlocked, "first worker's list started")
	require.NoError(t, first.Shutdown())
	assert.Equal(t, "PENDING", workflowStatus(t, url, handle.GetWorkflowID()))

	// When the next worker with the same ID starts.
	resumed := &fakeNotices{admins: []int64{recoveredID}}
	second := noticeWorker(t, url, resumed)
	t.Cleanup(func() { assert.NoError(t, second.Shutdown()) })

	// Then it lists and sends.
	again, err := dbos.RetrieveWorkflow[[]recipientOutcome](second.ctx, handle.GetWorkflowID())
	require.NoError(t, err)
	outcomes, err := again.GetResult(dbos.WithHandleTimeout(wait))
	require.NoError(t, err)
	assert.Equal(t, []recipientOutcome{{UserID: recoveredID, Outcome: account.NoticeSent}}, outcomes)
	_, firstSends := interrupted.calls()
	assert.Empty(t, firstSends)
}
