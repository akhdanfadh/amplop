package dbos

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A worker needs the workflow tables inbok migrate creates; it never
// creates them itself.
func TestWorkerUnmigrated(t *testing.T) {
	url := newDatabase(t)

	_, err := NewWorker(url, "worker", Services{}, discard)

	require.ErrorContains(t, err, "is at schema version 0")
}

// DBOS's own variables would replace the version and ID that decide which
// work a worker resumes, so a worker refuses to start with either set.
func TestWorkerDBOSOverrides(t *testing.T) {
	for _, name := range []string{"DBOS__APPVERSION", "DBOS__VMID"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "other")

			_, err := NewWorker("postgres://u:p@127.0.0.1:1/inbok", "worker", Services{}, discard)

			require.ErrorContains(t, err, name)
		})
	}
}

// A malformed database URL stops the worker with an error that hides the
// password; DBOS's own error would quote the URL.
func TestWorkerBadURL(t *testing.T) {
	_, err := NewWorker("postgres://inbok:SYNTHETIC_SECRET@localhost:bad/inbok", "worker", Services{}, discard)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SYNTHETIC_SECRET")
}

// A worker launches on a migrated database and shuts down.
func TestWorkerLaunch(t *testing.T) {
	worker, err := NewWorker(migrated(t), "worker", Services{}, discard)
	require.NoError(t, err)

	require.NoError(t, worker.Launch())

	require.NoError(t, worker.Shutdown())
}

// wait bounds every wait for a workflow in these tests.
const wait = 10 * time.Second

// Counters and gates for twoSteps. Each test that uses them resets them.
var (
	firstStepRuns atomic.Int32
	secondStarted chan struct{}
	secondStopped chan struct{}
	secondGate    chan struct{}
)

// twoSteps runs a first step, then a second that waits for secondGate or
// for the workflow to be cancelled, e.g. by Shutdown.
func twoSteps(ctx dbos.Context, input string) (string, error) {
	_, err := dbos.RunAsStep(ctx, func(context.Context) (int32, error) {
		return firstStepRuns.Add(1), nil
	}, dbos.WithStepName("first"))
	if err != nil {
		return "", err
	}

	return dbos.RunAsStep(ctx, func(stepCtx context.Context) (string, error) {
		secondStarted <- struct{}{}
		select {
		case <-secondGate:
			return input + " done", nil
		case <-stepCtx.Done():
			secondStopped <- struct{}{}
			return "", stepCtx.Err()
		}
	}, dbos.WithStepName("second"))
}

// A worker that stops during a workflow leaves it pending, not cancelled;
// the next worker with the same ID resumes it at the step that did not
// finish.
func TestWorkerResume(t *testing.T) {
	// Given a worker stopped while a workflow waits in its second step.
	url := migrated(t)
	firstStepRuns.Store(0)
	secondStarted = make(chan struct{}, 2)
	secondStopped = make(chan struct{}, 2)
	secondGate = make(chan struct{})

	first, err := NewWorker(url, "worker", Services{}, discard)
	require.NoError(t, err)
	dbos.RegisterWorkflow(first.ctx, twoSteps)
	require.NoError(t, first.Launch())
	_, err = dbos.RunWorkflow(first.ctx, twoSteps, "resume", dbos.WithWorkflowID("resume-1"))
	require.NoError(t, err)
	receive(t, secondStarted, "second step started")

	require.NoError(t, first.Shutdown())
	receive(t, secondStopped, "first worker's second step stopped")
	assert.Equal(t, "PENDING", workflowStatus(t, url, "resume-1"))

	// When another worker with the same ID launches, its second step free
	// to finish.
	close(secondGate)
	second, err := NewWorker(url, "worker", Services{}, discard)
	require.NoError(t, err)
	dbos.RegisterWorkflow(second.ctx, twoSteps)
	require.NoError(t, second.Launch())
	t.Cleanup(func() { assert.NoError(t, second.Shutdown()) })

	// Then the workflow finishes without running its first step again.
	handle, err := dbos.RetrieveWorkflow[string](second.ctx, "resume-1")
	require.NoError(t, err)
	result, err := handle.GetResult(dbos.WithHandleTimeout(wait))
	require.NoError(t, err)
	assert.Equal(t, "resume done", result)
	assert.Equal(t, int32(1), firstStepRuns.Load())
}

// stuckGate holds stuck's step until the test ends.
var stuckGate chan struct{}

// stuck runs one step that ignores cancellation until stuckGate closes.
func stuck(ctx dbos.Context, _ string) (string, error) {
	return dbos.RunAsStep(ctx, func(context.Context) (string, error) {
		secondStarted <- struct{}{}
		<-stuckGate
		return "", nil
	}, dbos.WithStepName("stuck"))
}

// Shutdown returns within its budget even when a step ignores
// cancellation, so the process exits before its container is killed.
func TestWorkerShutdownBudget(t *testing.T) {
	// Given a worker running a step that ignores cancellation.
	secondStarted = make(chan struct{}, 1)
	stuckGate = make(chan struct{})
	t.Cleanup(func() { close(stuckGate) })

	worker, err := NewWorker(migrated(t), "worker", Services{}, discard)
	require.NoError(t, err)
	dbos.RegisterWorkflow(worker.ctx, stuck)
	require.NoError(t, worker.Launch())
	_, err = dbos.RunWorkflow(worker.ctx, stuck, "")
	require.NoError(t, err)
	receive(t, secondStarted, "stuck step started")

	// When it shuts down.
	start := time.Now()
	_ = worker.Shutdown()

	// Then Shutdown returns within the budget.
	assert.Less(t, time.Since(start), shutdownBudget+time.Second)
}

// receive waits up to wait for a value on ch, failing the test with what
// as the reason if none comes.
func receive(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(wait):
		require.FailNow(t, "timed out waiting: "+what)
	}
}

// workflowStatus returns the status DBOS recorded for workflow id at url,
// e.g. PENDING.
func workflowStatus(t *testing.T, url, id string) string {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	defer conn.Close(t.Context())

	var status string
	err = conn.QueryRow(t.Context(), "SELECT status FROM dbos.workflow_status WHERE workflow_uuid = $1", id).Scan(&status)
	require.NoError(t, err)
	return status
}
