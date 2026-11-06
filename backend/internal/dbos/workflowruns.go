package dbos

import (
	"context"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
)

const (
	// succeededKept is how long a run that succeeded stays in DBOS's
	// tables: nearly every run succeeds and is rarely looked at again.
	succeededKept = 7 * 24 * time.Hour

	// failedKept is how long a run that failed stays: failures are rare,
	// and an operator may look into one days after it happened.
	failedKept = 30 * 24 * time.Hour

	// workflowRunsBatch is the most runs one [workflowRuns.DeleteExpired] deletes,
	// so one step's statements stay short however many runs have piled up.
	workflowRunsBatch = 1000
)

// Finished runs by outcome. Pending, enqueued, and delayed runs are never
// deleted: a worker still needs them to run or resume.
var (
	succeeded = []dbos.WorkflowStatusType{dbos.WorkflowStatusSuccess}
	failed    = []dbos.WorkflowStatusType{
		dbos.WorkflowStatusError, dbos.WorkflowStatusCancelled, dbos.WorkflowStatusMaxRecoveryAttemptsExceeded,
	}
)

// workflowRuns deletes DBOS's records of finished runs, with their steps'
// outputs, once they finished at least kept ago.
//
// DESIGN: DBOS keeps every run until something deletes it, and its Go SDK
// has no retention setting. Deleting finished runs is safe: the scheduler
// remembers its last run in its own row, and without automatic backfill it
// never runs an hour again whose run was deleted.
type workflowRuns struct {
	client   dbos.Client
	statuses []dbos.WorkflowStatusType
	kept     time.Duration
	batch    int
	now      func() time.Time
}

// newWorkflowRuns returns [workflowRuns] deleting, through client, runs with
// one of statuses that finished at least kept ago, a batch at a time.
func newWorkflowRuns(client dbos.Client, statuses []dbos.WorkflowStatusType, kept time.Duration) workflowRuns {
	return workflowRuns{client: client, statuses: statuses, kept: kept, batch: workflowRunsBatch, now: time.Now}
}

// DeleteExpired deletes up to one batch of runs with one of r's statuses
// that finished at least kept ago. It returns how many it deleted, whether
// more may remain, and DBOS's error.
func (r workflowRuns) DeleteExpired(context.Context) (int, bool, error) {
	// 1. List a batch, without inputs and outputs. DBOS's calls take its
	// own context, not the step's.
	runs, err := dbos.ListWorkflows(r.client,
		dbos.WithFilterStatus(r.statuses...),
		dbos.WithFilterCompletedBefore(r.now().Add(-r.kept)),
		dbos.WithFilterLimit(r.batch),
		dbos.WithFilterLoadInput(false),
		dbos.WithFilterLoadOutput(false),
	)
	if err != nil {
		return 0, false, err
	}
	if len(runs) == 0 {
		return 0, false, nil
	}

	// 2. Delete those runs, with their steps.
	//
	// NOTE: DeleteWorkflows deletes by ID, whatever the status is by then.
	// A run past its window that an operator puts back in the queue between
	// the list and the delete, by resuming a cancelled or dead-lettered run
	// or rewinding any finished one, keeps its ID and is deleted, so its
	// work never runs. DBOS has no conditional delete; no worker puts a
	// finished run back.
	ids := make([]string, len(runs))
	for i, run := range runs {
		ids[i] = run.ID
	}
	if err := dbos.DeleteWorkflows(r.client, ids); err != nil {
		return 0, false, err
	}
	// A short batch means none were left.
	return len(ids), len(ids) == r.batch, nil
}
