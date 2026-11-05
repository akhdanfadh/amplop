package dbos

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/repository/postgres"
)

var _ postgres.Workflows = (*Client)(nil)

// clientStack returns a migrated database with an administrator,
// admin@example.com, its storage saving workflows through a Client, and a
// connection for checks. All close when the test ends.
func clientStack(t *testing.T) (*postgres.DB, *pgx.Conn, string) {
	t.Helper()

	url := migrated(t)
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close(context.Background()) })
	_, err = conn.Exec(t.Context(), "INSERT INTO users (email, password_hash, role) VALUES ('admin@example.com', 'unused', 'admin')")
	require.NoError(t, err)

	client, err := NewClient(t.Context(), url, discard)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	db, err := postgres.Open(t.Context(), url)
	require.NoError(t, err)
	t.Cleanup(db.Close)
	return db.WithWorkflows(client), conn, url
}

// noticeWorkflows lists the recovery notice workflows saved in DBOS's
// tables, by status.
func noticeWorkflows(t *testing.T, conn *pgx.Conn) []string {
	t.Helper()
	rows, err := conn.Query(t.Context(), "SELECT status FROM dbos.workflow_status WHERE name = $1", recoveryNoticeWorkflow)
	require.NoError(t, err)
	statuses, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	return statuses
}

// A recovery saves its notice for the worker, which then sends it about
// that administrator and the time of the recovery's audit event.
func TestClientRecoveryNotice(t *testing.T) {
	// Given a recovery through storage that saves workflows with a Client.
	db, conn, url := clientStack(t)

	// When an administrator is recovered.
	_, err := account.NewRecovery(db).Recover(t.Context(), "admin@example.com", "a new recovery password")
	require.NoError(t, err)

	// Then its notice waits in the queue until a worker sends it.
	assert.Equal(t, []string{"ENQUEUED"}, noticeWorkflows(t, conn))
	var admin int64
	var occurredAt time.Time
	require.NoError(t, conn.QueryRow(t.Context(),
		"SELECT target_user_id, occurred_at FROM audit_events WHERE action = 'admin-recovered'").Scan(&admin, &occurredAt))
	notices := &fakeNotices{admins: []int64{admin}}
	worker := noticeWorker(t, url, notices)
	t.Cleanup(func() { assert.NoError(t, worker.Shutdown()) })
	require.Eventually(t, func() bool {
		_, sends := notices.calls()
		return len(sends) == 1
	}, wait, 50*time.Millisecond)
	recovered := notices.recovered()
	assert.Equal(t, admin, recovered[0].UserID)
	assert.WithinDuration(t, occurredAt, recovered[0].RecoveredAt, 0)
}

// A recovery that fails rolls its notice back with it: no workflow is
// saved, so no administrator hears of a recovery that did not happen.
func TestClientRecoveryRollback(t *testing.T) {
	// Given a recovery that fails as it commits, after the notice is saved.
	db, conn, _ := clientStack(t)
	_, err := conn.Exec(t.Context(), `CREATE FUNCTION reject_recovery() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'recovery rejected'; END $$`)
	require.NoError(t, err)
	_, err = conn.Exec(t.Context(), "CREATE CONSTRAINT TRIGGER reject_write AFTER INSERT ON audit_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_recovery()")
	require.NoError(t, err)

	// When an administrator is recovered.
	_, err = account.NewRecovery(db).Recover(t.Context(), "admin@example.com", "a new recovery password")

	// Then the recovery fails and no notice is saved.
	require.ErrorContains(t, err, "recovery rejected")
	assert.Empty(t, noticeWorkflows(t, conn))
}

// Cancelling a recovery stops an enqueue waiting on a lock, which DBOS
// alone would not, so the recovery rolls back and its database closes.
func TestClientRecoveryCancelled(t *testing.T) {
	// Given a recovery whose enqueue waits for a lock held elsewhere.
	db, conn, url := clientStack(t)
	observer, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	t.Cleanup(func() { observer.Close(context.Background()) })
	lock, err := conn.Begin(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Rollback(context.Background()) })
	_, err = lock.Exec(t.Context(), "LOCK TABLE dbos.workflow_status IN ACCESS EXCLUSIVE MODE")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := account.NewRecovery(db).Recover(ctx, "admin@example.com", "a new recovery password")
		done <- err
	}()
	require.Eventually(t, func() bool {
		var waiting bool
		err := observer.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE 'INSERT INTO %workflow_status%')`).Scan(&waiting)
		return err == nil && waiting
	}, wait, 10*time.Millisecond)

	// When the recovery is cancelled.
	cancel()

	// Then it fails at once, changing nothing, and the database closes.
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(wait):
		t.Fatal("the recovery still waits for the lock")
	}
	closed := make(chan struct{})
	go func() { db.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(wait):
		t.Fatal("closing the database still waits for the enqueue")
	}
	require.NoError(t, lock.Rollback(t.Context()))
	var events int
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM audit_events").Scan(&events))
	assert.Zero(t, events, "the recovery rolled back")
}

// A client needs the workflow tables inbok migrate creates.
func TestClientUnmigrated(t *testing.T) {
	_, err := NewClient(t.Context(), newDatabase(t), discard)

	require.ErrorContains(t, err, "is at schema version 0")
}
