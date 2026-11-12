package dbos

import (
	"testing"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// queueWorker launches a worker on url with no workflows of its own.
func queueWorker(t *testing.T, url string) *Worker {
	t.Helper()

	worker, err := NewWorker(url, "worker", Services{}, discard)
	require.NoError(t, err)
	require.NoError(t, worker.Launch())
	return worker
}

// Restarting the worker keeps each queue's saved settings, e.g. a
// concurrency limit set at runtime.
func TestQueueKept(t *testing.T) {
	for _, name := range []string{emailQueue, googleQueue, googleSendQueue} {
		t.Run(name, func(t *testing.T) {
			// Given a worker whose queue got a limit while it ran.
			url := migrated(t)
			first := queueWorker(t, url)
			queue, err := dbos.RetrieveQueue(first.ctx, name)
			require.NoError(t, err)
			require.NoError(t, queue.SetGlobalConcurrency(first.ctx, new(1)))
			require.NoError(t, first.Shutdown())

			// When the worker starts again.
			second := queueWorker(t, url)
			t.Cleanup(func() { assert.NoError(t, second.Shutdown()) })

			// Then the limit is still there.
			queue, err = dbos.RetrieveQueue(second.ctx, name)
			require.NoError(t, err)
			assert.Equal(t, new(1), queue.GetGlobalConcurrency())
		})
	}
}

// A worker takes runs only from the queues whose workflows it runs: without
// Google settings, sends stay queued for a worker that has them.
func TestListenedQueues(t *testing.T) {
	assert.Equal(t, []string{emailQueue}, listenedQueues(Services{}))
	assert.Equal(t, []string{emailQueue, googleQueue, googleSendQueue},
		listenedQueues(Services{AccountRemovals: &fakeRemovals{}, OutgoingMessages: newFakeSends()}))
	assert.Equal(t, []string{emailQueue, contentQueue}, listenedQueues(Services{MessageContent: &fakeContent{}}))
}
