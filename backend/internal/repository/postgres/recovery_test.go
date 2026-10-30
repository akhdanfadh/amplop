package postgres

import (
	"context"
	"testing"
	"time"

	"inbok/internal/account"
	"inbok/internal/passwordhash"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRecovery(t *testing.T) *confirmationFixture {
	t.Helper()
	f, _ := newRegeneration(t)
	_, err := f.conn.Exec(t.Context(), `
 INSERT INTO sign_in_challenges (user_id, token_hash, expires_at)
 SELECT id, decode(md5(email), 'hex'), now() + interval '5 minutes' FROM users;
 INSERT INTO sign_in_attempts (email, client_addr, created_at) VALUES
 ('admin@example.com', '127.0.0.1', now()),
 ('admin@example.com', '127.0.0.2', now()),
 ('other@example.com', '127.0.0.1', now()),
 (NULL, '127.0.0.1', now());
 INSERT INTO totp_attempts (user_id, created_at) SELECT id, now() FROM users WHERE role='member';`)
	require.NoError(t, err)
	return f
}

// Snapshot every affected table so rollback and account isolation checks cover
// values as well as row counts, including setup attempts with no email key.
func recoverySnapshot(t *testing.T, conn *pgx.Conn, userID int64) string {
	t.Helper()
	var snapshot string
	err := conn.QueryRow(t.Context(), `SELECT jsonb_build_array(
 (SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM users t WHERE id=$1),
 (SELECT jsonb_agg(to_jsonb(t) ORDER BY user_id) FROM user_totp t WHERE user_id=$1),
 (SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM totp_backup_codes t WHERE user_id=$1),
 (SELECT jsonb_agg(to_jsonb(t) ORDER BY token_hash) FROM sessions t WHERE user_id=$1),
 (SELECT jsonb_agg(to_jsonb(t) ORDER BY token_hash) FROM sign_in_challenges t WHERE user_id=$1),
 (SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM totp_attempts t WHERE user_id=$1),
 (SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM sign_in_attempts t WHERE email=(SELECT lower(email) FROM users WHERE id=$1) OR email IS NULL),
 (SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM audit_events t WHERE user_id=$1)
 )::text`, userID).Scan(&snapshot)
	require.NoError(t, err)
	return snapshot
}

func TestRecoveryPersistence(t *testing.T) {
	f := newRecovery(t)
	foreign := recoverySnapshot(t, f.conn, f.foreign.UserID)

	result, err := account.NewRecovery(f.db).Recover(t.Context(), " \tADMIN@EXAMPLE.COM\n", "a new recovery password")

	require.NoError(t, err)
	assert.Equal(t, account.RecoveryResult{Factors: 1, BackupCodes: 2, Sessions: 2, Challenges: 1, TOTPAttempts: 2, SignInAttempts: 2}, result)
	credentials, err := f.db.ReadCredentials(t.Context(), "admin@example.com")
	require.NoError(t, err)
	match, err := passwordhash.Verify("a new recovery password", credentials.PasswordHash)
	require.NoError(t, err)
	assert.True(t, match)
	assert.Equal(t, account.RoleAdmin, credentials.User.Role)
	for _, table := range []string{"user_totp", "totp_backup_codes", "sessions", "sign_in_challenges", "totp_attempts"} {
		var count int
		require.NoError(t, f.conn.QueryRow(t.Context(), "SELECT count(*) FROM "+table+" WHERE user_id=$1", f.state.UserID).Scan(&count))
		assert.Zero(t, count, table)
	}
	var attempts, events int
	require.NoError(t, f.conn.QueryRow(t.Context(), "SELECT count(*) FROM sign_in_attempts WHERE email='admin@example.com'").Scan(&attempts))
	assert.Zero(t, attempts)
	require.NoError(t, f.conn.QueryRow(t.Context(), "SELECT count(*) FROM audit_events WHERE user_id=$1 AND action='admin-recovered' AND occurred_at <= now()", f.state.UserID).Scan(&events))
	assert.Equal(t, 1, events)
	assert.Equal(t, foreign, recoverySnapshot(t, f.conn, f.foreign.UserID))

	result, err = account.NewRecovery(f.db).Recover(t.Context(), "admin@example.com", "another recovery password")
	require.NoError(t, err)
	assert.Zero(t, result, "recovery also works without a factor or sessions")
}

func TestRecoveryRollback(t *testing.T) {
	for _, c := range []struct{ name, trigger string }{
		{"audit", "CREATE TRIGGER reject_write BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_recovery()"},
		{"commit", "CREATE CONSTRAINT TRIGGER reject_write AFTER INSERT ON audit_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_recovery()"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newRecovery(t)
			before := recoverySnapshot(t, f.conn, f.state.UserID)
			_, err := f.conn.Exec(t.Context(), `CREATE FUNCTION reject_recovery() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'recovery rejected'; END $$`)
			require.NoError(t, err)
			_, err = f.conn.Exec(t.Context(), c.trigger)
			require.NoError(t, err)

			result, err := account.NewRecovery(f.db).Recover(t.Context(), "admin@example.com", "a new recovery password")

			require.Error(t, err)
			assert.Zero(t, result)
			assert.Equal(t, before, recoverySnapshot(t, f.conn, f.state.UserID))
		})
	}
}

func TestRecoveryRoleRecheck(t *testing.T) {
	f := newRecovery(t)
	ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
	defer cancel()
	tx, err := f.conn.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(t.Context())
	_, err = tx.Exec(ctx, "UPDATE users SET role='member' WHERE id=$1", f.state.UserID)
	require.NoError(t, err)
	results := make(chan error, 1)
	go func() {
		_, err := account.NewRecovery(f.db).Recover(ctx, "admin@example.com", "a new recovery password")
		results <- err
	}()

	require.Eventually(t, func() bool {
		var blocked bool
		err := f.db.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%LockRecoveryUser%')").Scan(&blocked)
		return err == nil && blocked
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, tx.Commit(ctx))
	before := recoverySnapshot(t, f.conn, f.state.UserID)

	require.ErrorIs(t, <-results, account.ErrRecoveryForbidden)
	assert.Equal(t, before, recoverySnapshot(t, f.conn, f.state.UserID))
}

func TestRecoveryMissingUser(t *testing.T) {
	db, _ := setupStorage(t)
	called := false
	result, err := db.UpdateRecovery(t.Context(), 1, func(account.User) (string, error) {
		called = true
		return "unused", nil
	})

	require.ErrorIs(t, err, account.ErrUserNotFound)
	assert.False(t, called)
	assert.Zero(t, result)
}
