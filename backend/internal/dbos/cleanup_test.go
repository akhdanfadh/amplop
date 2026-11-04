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
// cleanup schedule paused so only triggerCleanup runs it, shuts it down when
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
// and stops after maxCleanupBatches for rows that never run out.
func TestCleanupBatches(t *testing.T) {
	links := &expiredRows{batches: []int{3, 3, 1}}
	endless := &expiredRows{batches: []int{1}, more: true}
	worker := cleanupWorker(t, migrated(t), Services{EmailVerificationCleanup: links, RegistrationCleanup: endless})

	deleted, err := triggerCleanup(t, worker)
	require.NoError(t, err)

	assert.Equal(t, map[string]int{"email verification links": 7, "registration links": 1}, deleted)
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
		`INSERT INTO email_verification_links (token_hash, user_id, address, created_at, expires_at)
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
	}, first)
	assert.Equal(t, map[string]int{
		"email verification links": 0, "registration links": 0,
		"password reset links": 0, "password reset attempts": 0,
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
