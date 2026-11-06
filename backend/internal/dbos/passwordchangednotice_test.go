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

	"inbok/internal/passwordreset"
)

// changedAt is when the password changed in these tests.
var changedAt = time.Date(2026, 11, 5, 12, 30, 0, 0, time.UTC)

// noticeResult is one answer of [fakeChangedNotices].
type noticeResult struct {
	outcome passwordreset.Outcome
	err     error
}

// fakeChangedNotices answers each Send with the next of results, repeating
// the last; with none it reports the notice sent. It records each call's
// input.
type fakeChangedNotices struct {
	results []noticeResult

	mu    sync.Mutex
	calls []PasswordChangedNoticeInput
}

func (f *fakeChangedNotices) Send(_ context.Context, userID int64, at time.Time) (passwordreset.Outcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, PasswordChangedNoticeInput{UserID: userID, ChangedAt: at})
	if len(f.results) == 0 {
		return passwordreset.OutcomeSent, nil
	}
	result := f.results[min(len(f.calls), len(f.results))-1]
	return result.outcome, result.err
}

// sends returns each Send's input, in order.
func (f *fakeChangedNotices) sends() []PasswordChangedNoticeInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// changedNoticeWorker launches a worker on url sending password changed
// notices with notices.
func changedNoticeWorker(t *testing.T, url string, notices passwordChangedNotices) *Worker {
	t.Helper()

	worker, err := NewWorker(url, "worker", Services{PasswordChangedNotices: notices}, discard)
	require.NoError(t, err)
	require.NoError(t, worker.Launch())
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })
	return worker
}

// enqueueChangedNotice enqueues the notice of user 7's change at [changedAt]
// by name, as a client does, and returns its handle.
func enqueueChangedNotice(t *testing.T, worker *Worker) dbos.WorkflowHandle[passwordreset.Outcome] {
	t.Helper()

	handle, err := dbos.Enqueue[passwordreset.Outcome](worker.ctx, emailQueue, passwordChangedNoticeWorkflow,
		PasswordChangedNoticeInput{UserID: 7, ChangedAt: changedAt})
	require.NoError(t, err)
	return handle
}

// The job sends the notice for its user and change, the time surviving
// DBOS's JSON, and records the outcome.
func TestPasswordChangedNoticeSent(t *testing.T) {
	notices := &fakeChangedNotices{}
	worker := changedNoticeWorker(t, migrated(t), notices)

	outcome, err := enqueueChangedNotice(t, worker).GetResult(dbos.WithHandleTimeout(wait))

	require.NoError(t, err)
	assert.Equal(t, passwordreset.OutcomeSent, outcome)
	sends := notices.sends()
	require.Len(t, sends, 1)
	assert.Equal(t, int64(7), sends[0].UserID)
	assert.WithinDuration(t, changedAt, sends[0].ChangedAt, 0)
}

// A send that keeps failing is retried with the notice policy, then gives
// up, and the run fails with its error.
func TestPasswordChangedNoticeGivesUp(t *testing.T) {
	quickRetries(t, &noticeRetries)
	notices := &fakeChangedNotices{results: []noticeResult{{err: errors.New("connection refused")}}}
	worker := changedNoticeWorker(t, migrated(t), notices)

	_, err := enqueueChangedNotice(t, worker).GetResult(dbos.WithHandleTimeout(wait))

	require.ErrorContains(t, err, "connection refused")
	assert.Len(t, notices.sends(), 6, "the first attempt and 5 retries")
}
