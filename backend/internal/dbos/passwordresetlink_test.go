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

// fakeLinks answers each Send with the next of results, repeating the last;
// with none it reports a link sent. It records each call's input.
type fakeLinks struct {
	results []linkResult

	mu    sync.Mutex
	calls []PasswordResetLinkInput
}

type linkResult struct {
	outcome passwordreset.Outcome
	err     error
}

func (f *fakeLinks) Send(_ context.Context, attemptID, userID int64) (passwordreset.Outcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, PasswordResetLinkInput{AttemptID: attemptID, UserID: userID})
	if len(f.results) == 0 {
		return passwordreset.OutcomeSent, nil
	}
	result := f.results[min(len(f.calls), len(f.results))-1]
	return result.outcome, result.err
}

// sends returns each Send's input, in order.
func (f *fakeLinks) sends() []PasswordResetLinkInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// The send is retried 3 more times, waiting 1, 2, then 2 minutes; the docs
// promise it.
func TestPasswordResetLinkRetryPolicy(t *testing.T) {
	assert.Equal(t, retries{attempts: 3, base: time.Minute, ceiling: 2 * time.Minute}, linkRetries)
}

// linkWorker launches a worker on url sending reset links with links.
func linkWorker(t *testing.T, url string, links passwordResetLinks) *Worker {
	t.Helper()

	worker, err := NewWorker(url, "worker", Services{PasswordResetLinks: links}, discard)
	require.NoError(t, err)
	require.NoError(t, worker.Launch())
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })
	return worker
}

// enqueueLink enqueues the reset link job for input by name, as a client
// does, and returns its handle.
func enqueueLink(t *testing.T, worker *Worker, input PasswordResetLinkInput) dbos.WorkflowHandle[passwordreset.Outcome] {
	t.Helper()

	handle, err := dbos.Enqueue[passwordreset.Outcome](worker.ctx, emailQueue, passwordResetLinkWorkflow, input)
	require.NoError(t, err)
	return handle
}

// The job sends the link for its attempt and account and records the
// outcome.
func TestPasswordResetLinkSent(t *testing.T) {
	links := &fakeLinks{}
	worker := linkWorker(t, migrated(t), links)
	input := PasswordResetLinkInput{AttemptID: 4, UserID: 7}

	outcome, err := enqueueLink(t, worker, input).GetResult(dbos.WithHandleTimeout(wait))

	require.NoError(t, err)
	assert.Equal(t, passwordreset.OutcomeSent, outcome)
	assert.Equal(t, []PasswordResetLinkInput{input}, links.sends())
}

// A send that fails is retried, the same step running again, until it
// passes.
func TestPasswordResetLinkRetries(t *testing.T) {
	quickRetries(t, &linkRetries)
	failed := linkResult{err: errors.New("connection refused")}
	links := &fakeLinks{results: []linkResult{failed, failed, {outcome: passwordreset.OutcomeSent}}}
	worker := linkWorker(t, migrated(t), links)

	outcome, err := enqueueLink(t, worker, PasswordResetLinkInput{AttemptID: 4, UserID: 7}).GetResult(dbos.WithHandleTimeout(wait))

	require.NoError(t, err)
	assert.Equal(t, passwordreset.OutcomeSent, outcome)
	assert.Len(t, links.sends(), 3)
}

// A send that keeps failing gives up after its retries, and the run fails
// with its error.
func TestPasswordResetLinkGivesUp(t *testing.T) {
	quickRetries(t, &linkRetries)
	links := &fakeLinks{results: []linkResult{{err: errors.New("connection refused")}}}
	worker := linkWorker(t, migrated(t), links)

	_, err := enqueueLink(t, worker, PasswordResetLinkInput{AttemptID: 4, UserID: 7}).GetResult(dbos.WithHandleTimeout(wait))

	require.ErrorContains(t, err, "connection refused")
	assert.Len(t, links.sends(), 4, "the first attempt and 3 retries")
}
