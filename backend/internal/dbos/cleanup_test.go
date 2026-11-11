package dbos

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/connectedaccount"
	"inbok/internal/emailverification"
	"inbok/internal/passwordreset"
	"inbok/internal/registration"
	"inbok/internal/repository/postgres"
)

// expiredRows answers DeleteExpired with batches, one per call, then with
// an empty batch. It counts its calls.
type expiredRows struct {
	batches []int
	more    bool // every batch reports more, e.g. rows that never run out
	err     error
	calls   int
}

func (e *expiredRows) DeleteExpired(context.Context) (int, bool, error) {
	e.calls++
	if e.err != nil {
		return 0, false, e.err
	}
	if e.calls > len(e.batches) {
		return 0, e.more, nil
	}
	return e.batches[e.calls-1], e.calls < len(e.batches) || e.more, nil
}

// cleanupWorker launches a worker on url calling services, with the
// cleanup schedule paused so only [triggerCleanup] runs it, shuts it down when
// the test ends, and returns it.
func cleanupWorker(t *testing.T, url string, services Services) *Worker {
	t.Helper()

	worker, err := NewWorker(url, "worker", services, discard)
	require.NoError(t, err)
	// A run firing on the hour during a test would race with triggerCleanup.
	require.NoError(t, dbos.PauseSchedule(worker.ctx, cleanupSchedule))
	require.NoError(t, worker.Launch())
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })
	return worker
}

// triggerCleanup runs the cleanup schedule now and returns what it deleted
// by name, and its error.
func triggerCleanup(t *testing.T, worker *Worker) (map[string]int, error) {
	t.Helper()

	handle, err := dbos.TriggerSchedule[map[string]int](worker.ctx, cleanupSchedule)
	require.NoError(t, err)
	return handle.GetResult(dbos.WithHandleTimeout(wait))
}

// The worker schedules the cleanup every hour, on the hour in UTC.
func TestCleanupSchedule(t *testing.T) {
	worker := cleanupWorker(t, migrated(t), Services{})

	schedule, err := dbos.GetSchedule(worker.ctx, cleanupSchedule)
	require.NoError(t, err)

	assert.Equal(t, "0 0 * * * *", schedule.Schedule)
	assert.Equal(t, "UTC", schedule.CronTimezone)
	assert.Equal(t, cleanupWorkflow, schedule.WorkflowName)
}

// A run deletes batches until one reports no more, for each kind of rows,
// and stops after [maxCleanupBatches] for rows that never run out.
func TestCleanupBatches(t *testing.T) {
	links := &expiredRows{batches: []int{3, 3, 1}}
	endless := &expiredRows{batches: []int{1}, more: true}
	worker := cleanupWorker(t, migrated(t), Services{EmailVerificationCleanup: links, RegistrationCleanup: endless})

	deleted, err := triggerCleanup(t, worker)
	require.NoError(t, err)

	assert.Equal(t, map[string]int{
		"email verification links": 7, "registration links": 1,
		"succeeded workflow runs": 0, "failed workflow runs": 0,
	}, deleted)
	assert.Equal(t, 3, links.calls)
	assert.Equal(t, maxCleanupBatches, endless.calls)
}

// One kind failing does not stop the others; the run then fails, naming it.
func TestCleanupError(t *testing.T) {
	lost := errors.New("connection lost")
	broken := &expiredRows{err: lost}
	links := &expiredRows{batches: []int{2}}
	worker := cleanupWorker(t, migrated(t), Services{EmailVerificationCleanup: broken, RegistrationCleanup: links})

	_, err := triggerCleanup(t, worker)

	require.ErrorContains(t, err, "delete expired email verification links")
	assert.Equal(t, 1, links.calls)
}

// With the features' real cleanups, a run deletes links of every kind that
// expired an hour or more ago, and password reset attempts no limit has
// counted for an hour or more, and keeps the rest, including ones within
// that hour; running again deletes nothing more.
func TestCleanupLinks(t *testing.T) {
	// Given links of both kinds that expired an hour or more ago, less than
	// an hour ago, and not yet.
	url := migrated(t)
	db, err := postgres.Open(t.Context(), url)
	require.NoError(t, err)
	t.Cleanup(db.Close)
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close(context.Background()) })

	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	for _, sql := range []string{
		"INSERT INTO users (email, password_hash, role) VALUES ('member@example.org', 'unused', 'member')",
		`INSERT INTO email_verification_links (token_hash, user_id, email, created_at, expires_at)
		SELECT decode(hash, 'hex'), (SELECT id FROM users), 'member@example.org', expires_at - interval '24 hours', expires_at
		FROM (VALUES ('01', $1::timestamptz - interval '1 hour'), ('02', $1 - interval '2 hours'), ('03', $1 - interval '59 minutes'), ('06', $1 + interval '1 second')) AS v(hash, expires_at)`,
		`INSERT INTO registration_links (token_hash, email, created_at, expires_at)
		SELECT decode(hash, 'hex'), 'visitor@example.org', expires_at - interval '24 hours', expires_at
		FROM (VALUES ('04', $1::timestamptz - interval '1 hour'), ('05', $1 - interval '30 minutes')) AS v(hash, expires_at)`,
		`INSERT INTO password_reset_links (token_hash, user_id, email, created_at, expires_at)
		SELECT decode(hash, 'hex'), (SELECT id FROM users), 'member@example.org', expires_at - interval '1 hour', expires_at
		FROM (VALUES ('07', $1::timestamptz - interval '1 hour'), ('08', $1 - interval '59 minutes')) AS v(hash, expires_at)`,
		`INSERT INTO password_reset_attempts (client_prefix, created_at)
		VALUES ('old', $1::timestamptz - interval '25 hours'), ('within the hour', $1 - interval '24 hours 59 minutes')`,
	} {
		args := []any{now}
		if !strings.Contains(sql, "$1") {
			args = nil
		}
		_, err = conn.Exec(t.Context(), sql, args...)
		require.NoError(t, err)
	}

	clock := func() time.Time { return now }
	worker := cleanupWorker(t, url, Services{
		EmailVerificationCleanup:    emailverification.NewCleanup(db, clock),
		RegistrationCleanup:         registration.NewCleanup(db, clock),
		PasswordResetCleanup:        passwordreset.NewCleanup(db, clock),
		PasswordResetAttemptCleanup: passwordreset.NewAttemptCleanup(db, clock),
	})

	// When the cleanup runs twice.
	first, err := triggerCleanup(t, worker)
	require.NoError(t, err)
	second, err := triggerCleanup(t, worker)
	require.NoError(t, err)

	// Then the first deletes the links past the grace and the second
	// nothing.
	assert.Equal(t, map[string]int{
		"email verification links": 2, "registration links": 1,
		"password reset links": 1, "password reset attempts": 1,
		"succeeded workflow runs": 0, "failed workflow runs": 0,
	}, first)
	assert.Equal(t, map[string]int{
		"email verification links": 0, "registration links": 0,
		"password reset links": 0, "password reset attempts": 0,
		"succeeded workflow runs": 0, "failed workflow runs": 0,
	}, second)
	rows, err := conn.Query(t.Context(), `SELECT encode(token_hash, 'hex') FROM email_verification_links
		UNION ALL SELECT encode(token_hash, 'hex') FROM registration_links
		UNION ALL SELECT encode(token_hash, 'hex') FROM password_reset_links
		UNION ALL SELECT client_prefix FROM password_reset_attempts ORDER BY 1`)
	require.NoError(t, err)
	left, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	assert.Equal(t, []string{"03", "05", "06", "08", "within the hour"}, left)
}

// The cleanup deletes connect states that expired an hour or more ago.
func TestCleanupConnectStates(t *testing.T) {
	// Given states that expired an hour or more ago, less than an hour ago,
	// and not yet.
	url := migrated(t)
	db, err := postgres.Open(t.Context(), url)
	require.NoError(t, err)
	t.Cleanup(db.Close)
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close(context.Background()) })

	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	for _, sql := range []string{
		"INSERT INTO users (email, password_hash, role) VALUES ('member@example.org', 'unused', 'member')",
		"INSERT INTO sessions (token_hash, user_id, expires_at) SELECT '\\x01', id, now() + interval '1 day' FROM users",
		`INSERT INTO connect_states (state_hash, user_id, session_id, code_verifier, created_at, expires_at)
		SELECT decode(hash, 'hex'), s.user_id, s.id, 'verifier', v.expires_at - interval '10 minutes', v.expires_at FROM sessions s,
		(VALUES ('01', $1::timestamptz - interval '1 hour'), ('02', $1 - interval '59 minutes'), ('03', $1 + interval '1 second')) AS v(hash, expires_at)`,
	} {
		args := []any{now}
		if !strings.Contains(sql, "$1") {
			args = nil
		}
		_, err = conn.Exec(t.Context(), sql, args...)
		require.NoError(t, err)
	}
	worker := cleanupWorker(t, url, Services{ConnectStateCleanup: connectedaccount.NewStateCleanup(db, func() time.Time { return now })})

	// When the cleanup runs.
	deleted, err := triggerCleanup(t, worker)
	require.NoError(t, err)

	// Then only the state past the grace is gone.
	assert.Equal(t, 1, deleted["connect states"])
	rows, err := conn.Query(t.Context(), "SELECT encode(state_hash, 'hex') FROM connect_states ORDER BY 1")
	require.NoError(t, err)
	left, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	assert.Equal(t, []string{"02", "03"}, left)
}

// A run deletes DBOS's records of runs that succeeded at least 7 days ago
// and of runs that failed at least 30 days ago, steps included, and never
// a run that has not finished, however old.
func TestCleanupWorkflowRuns(t *testing.T) {
	// Given finished cleanup runs, aged and relabelled to cover each side of
	// both cutoffs, and one pending.
	day := 24 * time.Hour
	runs := []agedRun{
		{"SUCCESS", 8 * day},                         // goes
		{"SUCCESS", 6 * day},                         // stays
		{"ERROR", 31 * day},                          // goes
		{"ERROR", 8 * day},                           // stays
		{"CANCELLED", 31 * day},                      // goes
		{"MAX_RECOVERY_ATTEMPTS_EXCEEDED", 31 * day}, // goes
		{"PENDING", 90 * day},                        // stays: not finished
		{"ENQUEUED", 90 * day},                       // stays: not started
		{"DELAYED", 90 * day},                        // stays: not started
	}
	gone := []int{0, 2, 4, 5}
	kept := []int{1, 3, 6, 7, 8}
	url := migrated(t)
	worker := cleanupWorker(t, url, Services{})
	for range runs {
		_, err := triggerCleanup(t, worker)
		require.NoError(t, err)
	}
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close(context.Background()) })
	ids := workflowIDs(t, conn)
	require.Len(t, ids, len(runs))
	ageRuns(t, conn, ids, runs)
	for i, n := range stepCounts(t, conn, ids) {
		require.Positive(t, n, "run %d has steps to delete or keep", i)
	}

	// When the cleanup runs.
	deleted, err := triggerCleanup(t, worker)
	require.NoError(t, err)

	// Then only the runs past their cutoff are gone, with their steps, and
	// the rest keep theirs; the run that deleted them comes last.
	assert.Equal(t, 1, deleted["succeeded workflow runs"])
	assert.Equal(t, 3, deleted["failed workflow runs"])
	var keptIDs []string
	for _, i := range kept {
		keptIDs = append(keptIDs, ids[i])
	}
	assert.Equal(t, keptIDs, workflowIDs(t, conn)[:len(kept)])
	steps := stepCounts(t, conn, ids)
	for _, i := range gone {
		assert.Zero(t, steps[i], "run %d", i)
	}
	for _, i := range kept {
		assert.Positive(t, steps[i], "run %d", i)
	}
}

// A run finished exactly its window ago is deleted; one finished a
// millisecond later is kept.
func TestCleanupWorkflowRunsCutoff(t *testing.T) {
	// Given two runs that succeeded at the cutoff and just after it.
	url := migrated(t)
	worker := cleanupWorker(t, url, Services{})
	for range 2 {
		_, err := triggerCleanup(t, worker)
		require.NoError(t, err)
	}
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close(context.Background()) })
	ids := workflowIDs(t, conn)
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-succeededKept).UnixMilli()
	for i, completedAt := range []int64{cutoff, cutoff + 1} {
		_, err := conn.Exec(t.Context(), "UPDATE dbos.workflow_status SET completed_at = $1 WHERE workflow_uuid = $2", completedAt, ids[i])
		require.NoError(t, err)
	}
	runs := newWorkflowRuns(worker.ctx, succeeded, succeededKept)
	runs.now = func() time.Time { return now }

	// When it deletes.
	deleted, more, err := runs.DeleteExpired(t.Context())
	require.NoError(t, err)

	// Then only the run at the cutoff goes.
	assert.Equal(t, 1, deleted)
	assert.False(t, more)
	assert.Equal(t, []string{ids[1]}, workflowIDs(t, conn))
}

// stepCounts returns how many steps DBOS keeps for each of ids, in order.
func stepCounts(t *testing.T, conn *pgx.Conn, ids []string) []int {
	t.Helper()

	counts := make([]int, len(ids))
	for i, id := range ids {
		require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM dbos.operation_outputs WHERE workflow_uuid = $1", id).Scan(&counts[i]))
	}
	return counts
}

// One call deletes up to a batch of runs, and reports more only when it
// deleted a full batch.
func TestCleanupWorkflowRunsBatches(t *testing.T) {
	// Given three runs that succeeded 8 days ago.
	url := migrated(t)
	worker := cleanupWorker(t, url, Services{})
	for range 3 {
		_, err := triggerCleanup(t, worker)
		require.NoError(t, err)
	}
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close(context.Background()) })
	_, err = conn.Exec(t.Context(), "UPDATE dbos.workflow_status SET completed_at = $1", time.Now().Add(-8*24*time.Hour).UnixMilli())
	require.NoError(t, err)
	runs := newWorkflowRuns(worker.ctx, succeeded, succeededKept)
	runs.batch = 2

	// When it deletes batch after batch.
	type batch struct {
		deleted int
		more    bool
	}
	var batches []batch
	for range 3 {
		deleted, more, err := runs.DeleteExpired(t.Context())
		require.NoError(t, err)
		batches = append(batches, batch{deleted, more})
	}

	// Then a full batch reports more, a short one and an empty one none.
	assert.Equal(t, []batch{{2, true}, {1, false}, {0, false}}, batches)
}

// agedRun is a status and how long ago a run finished with it.
type agedRun struct {
	status string
	age    time.Duration
}

// ageRuns gives the run with each of ids the status and finish time of the
// run at the same index. An enqueued or delayed run moves to a queue no
// worker polls, so the test's worker does not run it again.
func ageRuns(t *testing.T, conn *pgx.Conn, ids []string, runs []agedRun) {
	t.Helper()

	for i, run := range runs {
		_, err := conn.Exec(t.Context(), `UPDATE dbos.workflow_status SET status = $1, completed_at = $2,
			queue_name = CASE WHEN $1 IN ('ENQUEUED', 'DELAYED') THEN 'inbok.test-unpolled' ELSE queue_name END
			WHERE workflow_uuid = $3`,
			run.status, time.Now().Add(-run.age).UnixMilli(), ids[i])
		require.NoError(t, err)
	}
}

// workflowIDs lists the IDs of every run in DBOS's tables, oldest first.
func workflowIDs(t *testing.T, conn *pgx.Conn) []string {
	t.Helper()

	rows, err := conn.Query(t.Context(), "SELECT workflow_uuid FROM dbos.workflow_status ORDER BY created_at, workflow_uuid")
	require.NoError(t, err)
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	return ids
}

// The hourly cleanup sweeps content no part names, after every other kind.
func TestCleanupUnusedContent(t *testing.T) {
	unused := &expiredRows{batches: []int{2}}
	worker := cleanupWorker(t, migrated(t), Services{MessageContentCleanup: unused})

	deleted, err := triggerCleanup(t, worker)
	require.NoError(t, err)

	assert.Equal(t, 2, deleted["unused message content"])
	names := make([]string, 0)
	for _, c := range worker.cleanups(Services{ConnectStateCleanup: unused, MessageContentCleanup: unused}) {
		names = append(names, c.name)
	}
	assert.Equal(t, "unused message content", names[len(names)-1])
}
