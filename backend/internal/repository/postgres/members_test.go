package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
)

// [membersNow] has nanoseconds, which saving drops: a deactivation at
// [membersNow] is saved at membersSaved.
var (
	membersNow   = time.Date(2026, 11, 2, 9, 30, 0, 123456789, time.UTC)
	membersSaved = time.Date(2026, 11, 2, 9, 30, 0, 123456000, time.UTC)
)

// memberAccounts are the accounts [newMemberAccounts] saves.
type memberAccounts struct {
	db        *DB
	conn      *pgx.Conn
	service   *account.Members
	admin     int64 // signed in
	member    int64 // verified, with a confirmed factor, signed in
	bystander int64 // signed in too, joined before member
}

// newMemberAccounts saves an administrator with an unconfirmed factor, a
// session, and a challenge; a verified member with a confirmed factor, a backup code, two sessions, a
// sign-in challenge, an email verification link, a password reset link, and
// a TOTP attempt; and a bystander member with a session, a challenge, and
// both kinds of link. It returns them
// with a service whose clock reads [membersNow].
func newMemberAccounts(t *testing.T) memberAccounts {
	t.Helper()

	db, conn := setupStorage(t)
	f := memberAccounts{db: db, conn: conn, service: account.NewMembers(db, func() time.Time { return membersNow })}
	insert := func(email, role, joined string) int64 {
		var id int64
		require.NoError(t, conn.QueryRow(t.Context(),
			"INSERT INTO users (email, password_hash, role, created_at) VALUES ($1, 'hash of '||$1, $2, $3) RETURNING id",
			email, role, joined).Scan(&id))
		return id
	}
	f.admin = insert("admin@example.com", "admin", "2026-01-01T00:00:00Z")
	f.member = insert("member@example.com", "member", "2026-03-01T00:00:00Z")
	f.bystander = insert("bystander@example.com", "member", "2026-02-01T00:00:00Z")
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{"UPDATE users SET email_verified_at = '2026-03-02T00:00:00Z' WHERE id = $1", []any{f.member}},
		{`INSERT INTO user_totp (user_id, key_id, secret_ciphertext, confirmed_at, started_at) VALUES
			($1, '1', 'admin ciphertext', NULL, now()),
			($2, '1', 'member ciphertext', '2026-03-03T00:00:00Z', '2026-03-03T00:00:00Z')`, []any{f.admin, f.member}},
		{"INSERT INTO totp_backup_codes (user_id, code_hash) VALUES ($1, 'backup code hash')", []any{f.member}},
		{"INSERT INTO totp_attempts (user_id, created_at) VALUES ($1, now())", []any{f.member}},
		{`INSERT INTO sessions (token_hash, user_id, expires_at)
			SELECT decode(md5(email || n), 'hex'), id, now() + interval '1 day'
			FROM users, generate_series(1, 2) AS n WHERE id = $1 OR n = 1`, []any{f.member}},
		{`INSERT INTO sign_in_challenges (token_hash, user_id, expires_at)
			SELECT decode(md5(email), 'hex'), id, now() + interval '5 minutes' FROM users`, nil},
		{`INSERT INTO email_verification_links (token_hash, user_id, email, created_at, expires_at)
			SELECT decode(md5(email), 'hex'), id, email, now(), now() + interval '1 day' FROM users WHERE id IN ($1, $2)`, []any{f.member, f.bystander}},
		{`INSERT INTO password_reset_links (token_hash, user_id, email, created_at, expires_at)
			SELECT decode(md5(email), 'hex'), id, email, now(), now() + interval '1 hour' FROM users WHERE id IN ($1, $2)`, []any{f.member, f.bystander}},
	} {
		_, err := conn.Exec(t.Context(), statement.sql, statement.args...)
		require.NoError(t, err)
	}
	return f
}

// memberSnapshot returns every row about the account with userID, including
// the audit events targeting it.
func memberSnapshot(t *testing.T, conn *pgx.Conn, userID int64) string {
	t.Helper()

	var snapshot string
	require.NoError(t, conn.QueryRow(t.Context(), `SELECT jsonb_build_array(
		(SELECT jsonb_agg(to_jsonb(t)) FROM users t WHERE id = $1),
		(SELECT jsonb_agg(to_jsonb(t)) FROM user_totp t WHERE user_id = $1),
		(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM totp_backup_codes t WHERE user_id = $1),
		(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM totp_attempts t WHERE user_id = $1),
		(SELECT jsonb_agg(to_jsonb(t) ORDER BY token_hash) FROM sessions t WHERE user_id = $1),
		(SELECT jsonb_agg(to_jsonb(t) ORDER BY token_hash) FROM sign_in_challenges t WHERE user_id = $1),
		(SELECT jsonb_agg(to_jsonb(t) ORDER BY token_hash) FROM email_verification_links t WHERE user_id = $1),
		(SELECT jsonb_agg(to_jsonb(t) ORDER BY token_hash) FROM password_reset_links t WHERE user_id = $1),
		(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM audit_events t WHERE target_user_id = $1)
	)::text`, userID).Scan(&snapshot))
	return snapshot
}

// memberRowCount counts the rows in table whose user_id is userID.
func memberRowCount(t *testing.T, conn *pgx.Conn, table string, userID int64) int {
	t.Helper()

	var count int
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM "+table+" WHERE user_id = $1", userID).Scan(&count))
	return count
}

// memberCredentials returns the saved password hash and factor of the
// account with userID.
func memberCredentials(t *testing.T, conn *pgx.Conn, userID int64) (hash, factor string) {
	t.Helper()

	require.NoError(t, conn.QueryRow(t.Context(),
		"SELECT u.password_hash, to_jsonb(t)::text FROM users u JOIN user_totp t ON t.user_id = u.id WHERE u.id = $1",
		userID).Scan(&hash, &factor))
	return hash, factor
}

// memberEvent is the audit event a status change records.
func memberEvent(action string, actor, target int64) auditEvent {
	return auditEvent{Action: action, Actor: &actor, Target: &target}
}

// The list holds every account, oldest first and then by ID, with what
// administrators see.
func TestMembersRead(t *testing.T) {
	f := newMemberAccounts(t)
	_, err := f.conn.Exec(t.Context(), "UPDATE users SET deactivated_at = $1 WHERE id = $2", membersSaved, f.bystander)
	require.NoError(t, err)
	// The administrator joined at the same time as the bystander, with a
	// lower ID.
	_, err = f.conn.Exec(t.Context(), "UPDATE users SET created_at = '2026-02-01T00:00:00Z' WHERE id = $1", f.admin)
	require.NoError(t, err)

	members, err := f.service.List(t.Context())
	require.NoError(t, err)

	deactivated := membersSaved
	want := []account.Member{
		{User: account.User{ID: f.admin, Email: "admin@example.com", Role: account.RoleAdmin}, JoinedAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)},
		{User: account.User{ID: f.bystander, Email: "bystander@example.com", Role: account.RoleMember}, JoinedAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), DeactivatedAt: &deactivated},
		{User: account.User{ID: f.member, Email: "member@example.com", Role: account.RoleMember}, JoinedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), EmailVerified: true, TOTPEnabled: true},
	}
	assert.Equal(t, want, members)
}

// Deactivating a member saves the time, ends their sessions, challenges, and
// links, keeps their credentials, and records who did it. The acting
// administrator and other members keep every row.
func TestMembersDeactivatePersistence(t *testing.T) {
	f := newMemberAccounts(t)
	admin, bystander := memberSnapshot(t, f.conn, f.admin), memberSnapshot(t, f.conn, f.bystander)
	hash, factor := memberCredentials(t, f.conn, f.member)

	member, err := f.service.Deactivate(t.Context(), f.admin, f.member)
	require.NoError(t, err)

	deactivated := membersSaved
	assert.Equal(t, account.Member{
		User:     account.User{ID: f.member, Email: "member@example.com", Role: account.RoleMember},
		JoinedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), EmailVerified: true, TOTPEnabled: true, DeactivatedAt: &deactivated,
	}, member)
	members, err := f.service.List(t.Context())
	require.NoError(t, err)
	assert.Contains(t, members, member)
	for _, table := range []string{"sessions", "sign_in_challenges", "email_verification_links", "password_reset_links"} {
		assert.Zero(t, memberRowCount(t, f.conn, table, f.member), table)
	}
	savedHash, savedFactor := memberCredentials(t, f.conn, f.member)
	assert.Equal(t, hash, savedHash)
	assert.Equal(t, factor, savedFactor)
	assert.Equal(t, 1, memberRowCount(t, f.conn, "totp_backup_codes", f.member))
	assert.Equal(t, []auditEvent{memberEvent("member-deactivated", f.admin, f.member)}, auditEvents(t, f.conn))
	assert.Equal(t, admin, memberSnapshot(t, f.conn, f.admin))
	assert.Equal(t, bystander, memberSnapshot(t, f.conn, f.bystander))
}

// Deactivating a deactivated member returns it unchanged and records no
// second event.
func TestMembersDeactivateRepeated(t *testing.T) {
	f := newMemberAccounts(t)
	first, err := f.service.Deactivate(t.Context(), f.admin, f.member)
	require.NoError(t, err)
	before := memberSnapshot(t, f.conn, f.member)

	again, err := account.NewMembers(f.db, func() time.Time { return membersNow.Add(time.Hour) }).Deactivate(t.Context(), f.admin, f.member)
	require.NoError(t, err)

	assert.Equal(t, first, again)
	assert.Equal(t, before, memberSnapshot(t, f.conn, f.member))
}

// Reactivating clears the time and records who did it; reactivating again
// changes nothing.
func TestMembersReactivatePersistence(t *testing.T) {
	// Given a deactivated member.
	f := newMemberAccounts(t)
	deactivated, err := f.service.Deactivate(t.Context(), f.admin, f.member)
	require.NoError(t, err)

	// When they are reactivated, and then reactivated again.
	member, err := f.service.Reactivate(t.Context(), f.admin, f.member)
	require.NoError(t, err)
	before := memberSnapshot(t, f.conn, f.member)
	again, err := f.service.Reactivate(t.Context(), f.admin, f.member)
	require.NoError(t, err)

	// Then the first reactivation is saved and recorded, and the second
	// changes nothing.
	deactivated.DeactivatedAt = nil
	assert.Equal(t, deactivated, member)
	assert.Equal(t, member, again)
	assert.Equal(t, before, memberSnapshot(t, f.conn, f.member))
	assert.Equal(t, []auditEvent{
		memberEvent("member-deactivated", f.admin, f.member),
		memberEvent("member-reactivated", f.admin, f.member),
	}, auditEvents(t, f.conn))
}

// A member promoted while the deactivation waits for their lock is an
// administrator by the time it runs, so nothing changes.
func TestMembersRoleRecheck(t *testing.T) {
	f := newMemberAccounts(t)
	ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
	defer cancel()
	tx, err := f.conn.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(t.Context())
	_, err = tx.Exec(ctx, "UPDATE users SET role = 'admin' WHERE id = $1", f.member)
	require.NoError(t, err)
	results := make(chan error, 1)
	go func() {
		_, err := f.service.Deactivate(ctx, f.admin, f.member)
		results <- err
	}()

	require.Eventually(t, func() bool {
		var blocked bool
		err := f.db.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%LockMemberUser%')").Scan(&blocked)
		return err == nil && blocked
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, tx.Commit(ctx))

	require.ErrorIs(t, <-results, account.ErrAdminTarget)
	var deactivated bool
	require.NoError(t, f.conn.QueryRow(t.Context(), "SELECT deactivated_at IS NOT NULL FROM users WHERE id = $1", f.member).Scan(&deactivated))
	assert.False(t, deactivated)
	assert.Equal(t, 2, memberRowCount(t, f.conn, "sessions", f.member))
	assert.Empty(t, auditEvents(t, f.conn))
}

// A failure while recording the event, or at commit, rolls back every
// write.
func TestMembersRollback(t *testing.T) {
	for _, c := range []struct{ name, trigger string }{
		{"audit", "CREATE TRIGGER reject_write BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_member_change()"},
		{"commit", "CREATE CONSTRAINT TRIGGER reject_write AFTER INSERT ON audit_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_member_change()"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newMemberAccounts(t)
			before := memberSnapshot(t, f.conn, f.member)
			_, err := f.conn.Exec(t.Context(), `CREATE FUNCTION reject_member_change() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'member change rejected'; END $$`)
			require.NoError(t, err)
			_, err = f.conn.Exec(t.Context(), c.trigger)
			require.NoError(t, err)

			member, err := f.service.Deactivate(t.Context(), f.admin, f.member)

			require.Error(t, err)
			assert.Zero(t, member)
			assert.Equal(t, before, memberSnapshot(t, f.conn, f.member))
		})
	}
}

// An action storage does not know rolls back without saving anything.
func TestMembersUnknownAction(t *testing.T) {
	f := newMemberAccounts(t)
	before := memberSnapshot(t, f.conn, f.member)

	_, err := f.db.UpdateMember(t.Context(), f.admin, f.member, func(account.Member) (account.MemberChange, error) {
		return account.MemberChange{Action: account.MemberReactivate + 1}, nil
	})

	require.Error(t, err)
	assert.Equal(t, before, memberSnapshot(t, f.conn, f.member))
}

// An unknown account is reported without calling update.
func TestMembersMissingUser(t *testing.T) {
	f := newMemberAccounts(t)
	called := false

	_, err := f.db.UpdateMember(t.Context(), f.admin, f.bystander+1, func(account.Member) (account.MemberChange, error) {
		called = true
		return account.MemberChange{}, nil
	})

	require.ErrorIs(t, err, account.ErrUserNotFound)
	assert.False(t, called)
}

func TestMembersMigrationDown(t *testing.T) {
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	defer conn.Close(t.Context())

	_, err = provider.DownTo(t.Context(), 16)
	require.NoError(t, err)

	assert.False(t, deactivationColumn(t, conn))

	_, err = provider.Up(t.Context())
	require.NoError(t, err)

	assert.True(t, deactivationColumn(t, conn))
}

// deactivationColumn reports whether the users column deactivated_at exists.
func deactivationColumn(t *testing.T, conn *pgx.Conn) bool {
	t.Helper()

	var exists bool
	require.NoError(t, conn.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'users' AND column_name = 'deactivated_at')`).Scan(&exists))
	return exists
}
