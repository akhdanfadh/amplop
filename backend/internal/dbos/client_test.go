package dbos

import (
	"context"
	"encoding/base64"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/connectedaccount"
	"inbok/internal/encryption"
	"inbok/internal/passwordreset"
	"inbok/internal/randomsecret"
	"inbok/internal/repository/postgres"
)

// clientStack returns a migrated database with an administrator,
// admin@example.com, its storage saving workflows through a [Client], and a
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

// linkWorkflows lists the reset link workflows saved in DBOS's tables, by
// status.
func linkWorkflows(t *testing.T, conn *pgx.Conn) []string {
	t.Helper()
	rows, err := conn.Query(t.Context(), "SELECT status FROM dbos.workflow_status WHERE name = $1", passwordResetLinkWorkflow)
	require.NoError(t, err)
	statuses, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	return statuses
}

// A reset request saves its job for the worker, which then sends the link
// for that attempt to the account with the email. The job's input holds no
// email, for an account or not.
func TestClientPasswordResetLink(t *testing.T) {
	// Given storage that saves workflows with a Client.
	db, conn, url := clientStack(t)
	requests := passwordreset.NewRequests(db, time.Now)

	// When an account's email, and an email with none, are asked about.
	client := netip.MustParseAddr("192.0.2.1")
	require.NoError(t, requests.Request(t.Context(), "admin@example.com", client))
	require.NoError(t, requests.Request(t.Context(), "nobody@example.com", client))

	// Then both jobs wait in the queue, their inputs holding IDs and no
	// email. DBOS saves an input in workflow_input, as base64-encoded JSON.
	assert.Equal(t, []string{"ENQUEUED", "ENQUEUED"}, linkWorkflows(t, conn))
	rows, err := conn.Query(t.Context(), `SELECT i.inputs FROM dbos.workflow_input i
		JOIN dbos.workflow_status s USING (workflow_uuid) WHERE s.name = $1`, passwordResetLinkWorkflow)
	require.NoError(t, err)
	encoded, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	require.Len(t, encoded, 2)
	for _, input := range encoded {
		decoded, err := base64.StdEncoding.DecodeString(input)
		require.NoError(t, err)
		assert.Contains(t, string(decoded), `"AttemptID"`)
		assert.NotContains(t, string(decoded), "@")
	}

	// And a worker sends each for its attempt and account.
	var admin int64
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT id FROM users WHERE email = 'admin@example.com'").Scan(&admin))
	rows, err = conn.Query(t.Context(), "SELECT id FROM password_reset_attempts ORDER BY id")
	require.NoError(t, err)
	attempts, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	require.NoError(t, err)
	links := &fakeLinks{}
	linkWorker(t, url, links)
	require.Eventually(t, func() bool { return len(links.sends()) == 2 }, wait, 50*time.Millisecond)
	assert.ElementsMatch(t, []PasswordResetLinkInput{
		{AttemptID: attempts[0], UserID: admin},
		{AttemptID: attempts[1]},
	}, links.sends())
}

// A reset request that fails as it commits rolls its job back with it, so
// no link is sent for a request that was not recorded.
func TestClientPasswordResetRollback(t *testing.T) {
	// Given storage whose attempts fail as they commit, after the job is saved.
	db, conn, _ := clientStack(t)
	_, err := conn.Exec(t.Context(), `CREATE FUNCTION reject_request() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'request rejected'; END $$`)
	require.NoError(t, err)
	_, err = conn.Exec(t.Context(), "CREATE CONSTRAINT TRIGGER reject_write AFTER INSERT ON password_reset_attempts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_request()")
	require.NoError(t, err)

	// When a reset is requested.
	err = passwordreset.NewRequests(db, time.Now).Request(t.Context(), "admin@example.com", netip.MustParseAddr("192.0.2.1"))

	// Then the request fails and no job is saved.
	require.ErrorContains(t, err, "request rejected")
	assert.Empty(t, linkWorkflows(t, conn))
}

// changedNoticeWorkflows lists the password changed notice workflows saved
// in DBOS's tables, by status.
func changedNoticeWorkflows(t *testing.T, conn *pgx.Conn) []string {
	t.Helper()
	rows, err := conn.Query(t.Context(), "SELECT status FROM dbos.workflow_status WHERE name = $1", passwordChangedNoticeWorkflow)
	require.NoError(t, err)
	statuses, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	return statuses
}

// A completed reset saves its notice for the worker, which then tells that
// user, at the time the reset was checked. The job's input holds no email.
func TestClientPasswordChangedNotice(t *testing.T) {
	// Given a live reset link for the administrator, through storage that
	// saves workflows with a Client.
	db, conn, url := clientStack(t)
	var admin int64
	require.NoError(t, conn.QueryRow(t.Context(), "UPDATE users SET email_verified_at = now() WHERE email = 'admin@example.com' RETURNING id").Scan(&admin))
	secret, hash := randomsecret.New()
	_, err := conn.Exec(t.Context(), `INSERT INTO password_reset_links (token_hash, user_id, email, created_at, expires_at)
		VALUES ($1, $2, 'admin@example.com', now(), now() + interval '1 hour')`, hash[:], admin)
	require.NoError(t, err)
	resetAt := time.Now().UTC()

	// When the link resets the password.
	_, err = passwordreset.NewCompletion(db, func() time.Time { return resetAt }).Complete(t.Context(), secret, "admin@example.com", "a new password for the reset")
	require.NoError(t, err)

	// Then its notice waits in the queue, holding no email, until a worker
	// sends it.
	assert.Equal(t, []string{"ENQUEUED"}, changedNoticeWorkflows(t, conn))
	var input string
	require.NoError(t, conn.QueryRow(t.Context(), `SELECT i.inputs FROM dbos.workflow_input i
		JOIN dbos.workflow_status s USING (workflow_uuid) WHERE s.name = $1`, passwordChangedNoticeWorkflow).Scan(&input))
	decoded, err := base64.StdEncoding.DecodeString(input)
	require.NoError(t, err)
	assert.Contains(t, string(decoded), `"UserID"`)
	assert.NotContains(t, string(decoded), "@")
	notices := &fakeChangedNotices{}
	changedNoticeWorker(t, url, notices)
	require.Eventually(t, func() bool { return len(notices.sends()) == 1 }, wait, 50*time.Millisecond)
	sent := notices.sends()[0]
	assert.Equal(t, admin, sent.UserID)
	assert.WithinDuration(t, resetAt, sent.ChangedAt, 0)
}

// A reset that fails as it commits rolls its notice back with it, so no
// one hears of a password change that did not happen.
func TestClientPasswordChangedNoticeRollback(t *testing.T) {
	// Given a live reset link, and audit events that fail as they commit,
	// after the notice is saved.
	db, conn, _ := clientStack(t)
	var admin int64
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT id FROM users WHERE email = 'admin@example.com'").Scan(&admin))
	secret, hash := randomsecret.New()
	_, err := conn.Exec(t.Context(), `INSERT INTO password_reset_links (token_hash, user_id, email, created_at, expires_at)
		VALUES ($1, $2, 'admin@example.com', now(), now() + interval '1 hour')`, hash[:], admin)
	require.NoError(t, err)
	_, err = conn.Exec(t.Context(), `CREATE FUNCTION reject_reset() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reset rejected'; END $$`)
	require.NoError(t, err)
	_, err = conn.Exec(t.Context(), "CREATE CONSTRAINT TRIGGER reject_write AFTER INSERT ON audit_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_reset()")
	require.NoError(t, err)

	// When the link resets the password.
	_, err = passwordreset.NewCompletion(db, time.Now).Complete(t.Context(), secret, "admin@example.com", "a new password for the reset")

	// Then the reset fails and no notice is saved.
	require.ErrorContains(t, err, "reset rejected")
	assert.Empty(t, changedNoticeWorkflows(t, conn))
}

// syncWorkflows lists the sync workflows saved in DBOS's tables:
// each one's status, queue, deduplication ID, and decoded input. DBOS saves
// an input in workflow_input, as base64-encoded JSON.
func syncWorkflows(t *testing.T, conn *pgx.Conn) [][4]string {
	t.Helper()
	rows, err := conn.Query(t.Context(), `
		SELECT s.status, coalesce(s.queue_name, ''), coalesce(s.deduplication_id, ''), coalesce(i.inputs, '')
		FROM dbos.workflow_status s LEFT JOIN dbos.workflow_input i USING (workflow_uuid)
		WHERE s.name = $1`, syncWorkflow)
	require.NoError(t, err)
	var saved [][4]string
	for rows.Next() {
		var w [4]string
		require.NoError(t, rows.Scan(&w[0], &w[1], &w[2], &w[3]))
		decoded, err := base64.StdEncoding.DecodeString(w[3])
		require.NoError(t, err)
		w[3] = string(decoded)
		saved = append(saved, w)
	}
	require.NoError(t, rows.Err())
	return saved
}

// connectAdmin connects a Gmail account for admin@example.com through db.
func connectAdmin(t *testing.T, db *postgres.DB, conn *pgx.Conn) (connectedaccount.Account, error) {
	t.Helper()
	ring, err := encryption.New("1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	require.NoError(t, err)
	var admin int64
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT id FROM users WHERE email = 'admin@example.com'").Scan(&admin))
	identity := connectedaccount.Identity{Subject: "100000000000000000001", Email: "admin@example.com"}
	return connectedaccount.NewService(db, ring).Connect(t.Context(), admin, identity, "refresh-1")
}

// Connecting an account saves its sync on the Google queue, with the
// account's deduplication ID and only its ID as input; a second sync of the
// account is not saved while the first waits, and leaves its transaction
// usable.
func TestClientSync(t *testing.T) {
	// Given storage that saves workflows with a Client.
	db, conn, url := clientStack(t)

	// When an account is connected, and its sync enqueued again.
	account, err := connectAdmin(t, db, conn)
	require.NoError(t, err)
	client, err := NewClient(t.Context(), url, discard)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	tx, err := conn.Begin(t.Context())
	require.NoError(t, err)
	enqueued, err := client.EnqueueSync(t.Context(), tx, account.ID)
	require.NoError(t, err)
	_, usable := tx.Exec(t.Context(), "SELECT 1")
	require.NoError(t, tx.Commit(t.Context()))

	// Then one sync waits, and the second was not saved.
	assert.False(t, enqueued)
	require.NoError(t, usable)
	saved := syncWorkflows(t, conn)
	require.Len(t, saved, 1)
	assert.Equal(t, "ENQUEUED", saved[0][0])
	assert.Equal(t, googleQueue, saved[0][1])
	assert.Equal(t, syncDeduplication(account.ID), saved[0][2])
	assert.Contains(t, saved[0][3], `"AccountID":`+strconv.FormatInt(account.ID, 10))
}

// A connect that fails as it commits rolls its sync back with it, so no job
// copies an account that was never saved.
func TestClientSyncRollback(t *testing.T) {
	// Given storage whose sync states fail as they commit, after the job is
	// saved.
	db, conn, _ := clientStack(t)
	_, err := conn.Exec(t.Context(), `CREATE FUNCTION reject_state() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'connect rejected'; END $$`)
	require.NoError(t, err)
	_, err = conn.Exec(t.Context(), "CREATE CONSTRAINT TRIGGER reject_write AFTER INSERT ON sync_states DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_state()")
	require.NoError(t, err)

	// When an account is connected.
	_, err = connectAdmin(t, db, conn)

	// Then the connect fails, and neither the account nor its job is saved.
	require.ErrorContains(t, err, "connect rejected")
	assert.Empty(t, syncWorkflows(t, conn))
	var accounts int
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM connected_accounts").Scan(&accounts))
	assert.Equal(t, 0, accounts)
}
