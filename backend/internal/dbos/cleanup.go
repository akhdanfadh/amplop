package dbos

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
)

const (
	// cleanupWorkflow and cleanupSchedule name the cleanup in DBOS's
	// tables, where runs and the schedule outlive the code that made them.
	cleanupWorkflow = "inbok.cleanup"
	cleanupSchedule = "inbok.cleanup.hourly"

	// cleanupCron fires every hour on the hour, read in UTC: in a zone
	// with a half-hour offset, local hours fall elsewhere. DBOS's cron has
	// a seconds field first.
	cleanupCron = "0 0 * * * *"

	// maxCleanupBatches bounds one run's batches for each kind of rows;
	// what is left waits for the next run.
	maxCleanupBatches = 100
)

// expired deletes one bounded batch of a feature's expired rows, e.g.
// [inbok/internal/emailverification.Cleanup].
type expired interface {
	// DeleteExpired returns how many rows it deleted and whether more may
	// remain.
	DeleteExpired(ctx context.Context) (int, bool, error)
}

// cleanup is one kind of expired rows the cleanup deletes. name labels its
// steps and its count in the run's output, e.g. "registration links".
type cleanup struct {
	name string
	rows expired
}

// cleanups lists the kinds of expired rows a run deletes, in order: the
// features' cleanups in s, leaving out nil ones, and DBOS's own finished
// workflow runs.
//
// DESIGN: Keep each name, and add new kinds at the end, after "failed
// workflow runs", whatever they delete: a run a previous release started
// resumes on this code, and DBOS fails a step whose name differs from the
// one it recorded at that position.
func (w *Worker) cleanups(s Services) []cleanup {
	all := []cleanup{
		{name: "email verification links", rows: s.EmailVerificationCleanup},
		{name: "registration links", rows: s.RegistrationCleanup},
		{name: "password reset links", rows: s.PasswordResetCleanup},
		{name: "password reset attempts", rows: s.PasswordResetAttemptCleanup},
		{name: "succeeded workflow runs", rows: newWorkflowRuns(w.ctx, succeeded, succeededKept)},
		{name: "failed workflow runs", rows: newWorkflowRuns(w.ctx, failed, failedKept)},
	}
	return slices.DeleteFunc(all, func(c cleanup) bool { return c.rows == nil })
}

// registerCleanup registers the workflow that deletes the expired rows of
// each of cleanups, in order, and schedules it hourly. It returns DBOS's
// error saving the schedule.
//
// DESIGN: Each batch is one step, so a worker that stops during a run
// resumes after the last finished batch, and deleting a batch again is
// harmless. The schedule waits for the next hour instead of making up for
// hours the worker was down: a run works through the whole backlog, up to
// maxCleanupBatches per kind, and the next runs take what it left.
func (w *Worker) registerCleanup(cleanups []cleanup) error {
	run := func(ctx dbos.Context, _ dbos.ScheduledWorkflowInput) (any, error) {
		return runCleanup(ctx, cleanups, w.logger)
	}
	dbos.RegisterWorkflow(w.ctx, run, dbos.WithWorkflowName(cleanupWorkflow))

	return dbos.ApplySchedules(w.ctx, []dbos.ScheduleSpec{{
		ScheduleName: cleanupSchedule,
		Schedule:     cleanupCron,
		CronTimezone: "UTC",
		WorkflowName: cleanupWorkflow,
	}})
}

// runCleanup runs the cleanup once: it deletes each cleanup's expired rows
// and returns how many it deleted, by name. A cleanup that fails is logged
// to logger and the rest still run; the errors are then returned joined.
func runCleanup(ctx dbos.Context, cleanups []cleanup, logger *slog.Logger) (map[string]int, error) {
	deleted := make(map[string]int, len(cleanups))
	var errs []error
	for _, c := range cleanups {
		n, err := deleteBatches(ctx, c)
		deleted[c.name] = n
		if err != nil {
			logger.WarnContext(ctx, "cleanup failed", "rows", c.name, "deleted", n, "err", err)
			errs = append(errs, fmt.Errorf("delete expired %s: %w", c.name, err))
			continue
		}
		if n > 0 {
			logger.InfoContext(ctx, "deleted expired rows", "rows", c.name, "deleted", n)
		}
	}
	return deleted, errors.Join(errs...)
}

// batch is what one cleanup step deleted, as DBOS records it.
type batch struct {
	Deleted int
	More    bool
}

// deleteBatches deletes c's expired rows, one batch per step, until a batch
// reports no more or maxCleanupBatches have run. It returns how many it
// deleted and the first step's error.
func deleteBatches(ctx dbos.Context, c cleanup) (int, error) {
	total := 0
	for range maxCleanupBatches {
		b, err := dbos.RunAsStep(ctx, func(stepCtx context.Context) (batch, error) {
			n, more, err := c.rows.DeleteExpired(stepCtx)
			return batch{Deleted: n, More: more}, err
		}, dbos.WithStepName("delete expired "+c.name))
		if err != nil {
			return total, err
		}
		total += b.Deleted
		if !b.More {
			break
		}
	}
	return total, nil
}
