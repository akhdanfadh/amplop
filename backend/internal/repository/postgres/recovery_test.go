package postgres

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"inbok/internal/account"
	"inbok/internal/passwordhash"
	"inbok/internal/passwordreset"
	"inbok/internal/session"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tableWorkflows saves each recovery notice as a row of
// test_recovery_notices in the transaction it is given, as DBOS's enqueue
// does, so a test sees whether the notice committed. err fails the enqueue.
// It implements only [RecoveryWorkflows]: a call to another feature's job
// reaches the nil [Workflows] and panics, failing the test.
type tableWorkflows struct {
	Workflows
	err error
}

func (w tableWorkflows) EnqueueRecoveryNotice(ctx context.Context, tx pgx.Tx, userID int64, _ time.Time) error {
	if w.err != nil {
		return w.err
	}
	_, err := tx.Exec(ctx, "INSERT INTO test_recovery_notices (user_id) VALUES ($1)", userID)
	return err
}

// recoveryNotices lists the user IDs of the saved recovery notices.
func recoveryNotices(t *testing.T, conn *pgx.Conn) []int64 {
	t.Helper()
	rows, err := conn.Query(t.Context(), "SELECT user_id FROM test_recovery_notices ORDER BY user_id")
	require.NoError(t, err)
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	require.NoError(t, err)
	return ids
}

func newRecovery(t *testing.T) *confirmationFixture {
	t.Helper()
	f, _ := newRegeneration(t)
	f.db = f.db.WithWorkflows(tableWorkflows{})
	_, err := f.conn.Exec(t.Context(), `
 CREATE TABLE test_recovery_notices (user_id bigint NOT NULL);
 INSERT INTO sign_in_challenges (user_id, token_hash, expires_at)
 SELECT id, decode(md5(email), 'hex'), now() + interval '5 minutes' FROM users;
 INSERT INTO sign_in_attempts (email, client_prefix, created_at) VALUES
 ('admin@example.com', '127.0.0.1', now()),
 ('admin@example.com', '127.0.0.2', now()),
 ('other@example.com', '127.0.0.1', now()),
 (NULL, '127.0.0.1', now());
 INSERT INTO totp_attempts (user_id, created_at) SELECT id, now() FROM users WHERE role='member';
 INSERT INTO password_reset_links (token_hash, user_id, email, created_at, expires_at)
 SELECT decode(md5(email), 'hex'), id, email, now(), now() + interval '1 hour' FROM users;`)
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
 (SELECT jsonb_agg(to_jsonb(t) ORDER BY token_hash) FROM password_reset_links t WHERE user_id=$1),
 (SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM sign_in_attempts t WHERE email=(SELECT email FROM users WHERE id=$1) OR email IS NULL),
 (SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM audit_events t WHERE target_user_id=$1)
 )::text`, userID).Scan(&snapshot)
	require.NoError(t, err)
	return snapshot
}

func TestRecoveryPersistence(t *testing.T) {
	f := newRecovery(t)
	foreign := recoverySnapshot(t, f.conn, f.foreign.UserID)

	result, err := account.NewRecovery(f.db).Recover(t.Context(), " \tADMIN@EXAMPLE.COM\t", "a new recovery password")

	require.NoError(t, err)
	assert.Equal(t, account.RecoveryResult{Factors: 1, BackupCodes: 2, Sessions: 2, Challenges: 1, TOTPAttempts: 2, SignInAttempts: 2, ResetLinks: 1}, result)
	credentials, err := f.db.ReadCredentials(t.Context(), "admin@example.com")
	require.NoError(t, err)
	match, err := passwordhash.Verify(t.Context(), "a new recovery password", credentials.PasswordHash)
	require.NoError(t, err)
	assert.True(t, match)
	assert.Equal(t, account.RoleAdmin, credentials.User.Role)
	for _, table := range []string{"user_totp", "totp_backup_codes", "sessions", "sign_in_challenges", "totp_attempts", "password_reset_links"} {
		var count int
		require.NoError(t, f.conn.QueryRow(t.Context(), "SELECT count(*) FROM "+table+" WHERE user_id=$1", f.state.UserID).Scan(&count))
		assert.Zero(t, count, table)
	}
	var attempts, events int
	require.NoError(t, f.conn.QueryRow(t.Context(), "SELECT count(*) FROM sign_in_attempts WHERE email='admin@example.com'").Scan(&attempts))
	assert.Zero(t, attempts)
	require.NoError(t, f.conn.QueryRow(t.Context(), "SELECT count(*) FROM audit_events WHERE target_user_id=$1 AND actor_user_id IS NULL AND action='admin-recovered' AND occurred_at <= now()", f.state.UserID).Scan(&events))
	assert.Equal(t, 1, events)
	assert.Equal(t, foreign, recoverySnapshot(t, f.conn, f.foreign.UserID))

	assert.Equal(t, []int64{f.state.UserID}, recoveryNotices(t, f.conn), "the notice commits with the recovery")

	result, err = account.NewRecovery(f.db).Recover(t.Context(), "admin@example.com", "another recovery password")
	require.NoError(t, err)
	assert.Zero(t, result, "recovery also works without a factor or sessions")
	assert.Len(t, recoveryNotices(t, f.conn), 2, "each recovery saves its own notice")
}

// A notice that cannot be saved fails the recovery, which changes nothing,
// and so does recovering without a way to save one.
func TestRecoveryNoticeNotSaved(t *testing.T) {
	cases := map[string]Workflows{
		"enqueue failed": tableWorkflows{err: errors.New("workflow tables missing")},
		"no workflows":   nil,
	}
	for name, workflows := range cases {
		t.Run(name, func(t *testing.T) {
			f := newRecovery(t)
			before := recoverySnapshot(t, f.conn, f.state.UserID)

			_, err := account.NewRecovery(f.db.WithWorkflows(workflows)).Recover(t.Context(), "admin@example.com", "a new recovery password")

			require.Error(t, err)
			assert.Equal(t, before, recoverySnapshot(t, f.conn, f.state.UserID))
			assert.Empty(t, recoveryNotices(t, f.conn))
		})
	}
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
			assert.Empty(t, recoveryNotices(t, f.conn), "the notice rolls back with the recovery")
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

type recoveryAccounts struct {
	service *account.Service
	checked chan struct{}
	resume  chan struct{}
}

func (p *recoveryAccounts) Authenticate(ctx context.Context, email, password string) (account.Credentials, error) {
	credentials, err := p.service.Authenticate(ctx, email, password)
	close(p.checked)
	select {
	case <-p.resume:
	case <-ctx.Done():
		return account.Credentials{}, ctx.Err()
	}
	return credentials, err
}

func TestRecoveryStaleSignIn(t *testing.T) {
	for _, password := range []string{"the new recovery password", "the old recovery password"} {
		t.Run(password, func(t *testing.T) {
			f := newRecovery(t)
			ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
			defer cancel()
			_, err := f.conn.Exec(ctx, "UPDATE users SET password_hash=$1 WHERE id=$2", hashPassword(t, "the old recovery password"), f.state.UserID)
			require.NoError(t, err)
			accounts := &recoveryAccounts{service: account.NewService(f.db, ""), checked: make(chan struct{}), resume: make(chan struct{})}
			clock := time.Now
			service := session.NewService(f.db, accounts, session.NewThrottle(f.db, clock), clock)
			type outcome struct {
				result session.SignedIn
				err    error
			}
			results := make(chan outcome, 1)
			go func() {
				result, err := service.StartSignIn(ctx, "admin@example.com", "the old recovery password", session.Device{Addr: netip.MustParseAddr("127.0.0.1")})
				results <- outcome{result, err}
			}()
			select {
			case <-accounts.checked:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}

			_, err = account.NewRecovery(f.db).Recover(ctx, "admin@example.com", password)
			close(accounts.resume)
			require.NoError(t, err)
			got := <-results

			require.ErrorIs(t, got.err, account.ErrInvalidCredentials)
			assert.Zero(t, got.result)
			for _, table := range []string{"sessions", "sign_in_challenges"} {
				var count int
				require.NoError(t, f.conn.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE user_id=$1", f.state.UserID).Scan(&count))
				assert.Zero(t, count, table)
			}
			fresh := session.NewService(f.db, account.NewService(f.db, ""), session.NewThrottle(f.db, clock), clock)
			issued, err := fresh.StartSignIn(ctx, "admin@example.com", password, session.Device{Addr: netip.MustParseAddr("127.0.0.1")})
			require.NoError(t, err)
			assert.Equal(t, session.SignInStepComplete, issued.Step)
		})
	}
}

// A reset link sent before recovery cannot replace the password the
// operator chose.
func TestRecoveryEndsResetLinks(t *testing.T) {
	f := newRecovery(t)
	secret, _ := saveResetLink(t, f.conn, f.state.UserID, "admin@example.com")
	_, err := account.NewRecovery(f.db).Recover(t.Context(), "admin@example.com", "a new recovery password")
	require.NoError(t, err)

	_, err = resetCompletion(t, f.db, func() time.Time { return resetNow }).Complete(t.Context(), secret, "admin@example.com", resetPassword)

	require.ErrorIs(t, err, passwordreset.ErrLinkInvalid)
	credentials, err := f.db.ReadCredentials(t.Context(), "admin@example.com")
	require.NoError(t, err)
	match, err := passwordhash.Verify(t.Context(), "a new recovery password", credentials.PasswordHash)
	require.NoError(t, err)
	assert.True(t, match, "the recovered password stays")
}

// A notice reads its recipient's current email and whether they are
// active, and the recovered account's email, empty when it is gone; a
// missing recipient is [account.ErrUserNotFound].
func TestRecoveryNoticeRead(t *testing.T) {
	// Given a recovered administrator and another, later deactivated.
	db, conn := setupStorage(t)
	recovered := verificationUser(t, conn, "admin@example.com")
	other := verificationUser(t, conn, "other@example.com")

	own, err := db.ReadRecoveryNotice(t.Context(), recovered, recovered)
	require.NoError(t, err)
	alert, err := db.ReadRecoveryNotice(t.Context(), recovered, other)
	require.NoError(t, err)
	_, err = conn.Exec(t.Context(), "UPDATE users SET deactivated_at = now() WHERE id = $1", other)
	require.NoError(t, err)
	deactivated, err := db.ReadRecoveryNotice(t.Context(), recovered, other)
	require.NoError(t, err)
	gone, err := db.ReadRecoveryNotice(t.Context(), other+1, recovered)
	require.NoError(t, err)
	_, missingErr := db.ReadRecoveryNotice(t.Context(), recovered, other+1)

	assert.Equal(t, account.RecoveryNoticeState{RecipientEmail: "admin@example.com", RecipientActive: true, RecoveredEmail: "admin@example.com"}, own)
	assert.Equal(t, account.RecoveryNoticeState{RecipientEmail: "other@example.com", RecipientActive: true, RecoveredEmail: "admin@example.com"}, alert)
	assert.False(t, deactivated.RecipientActive)
	assert.Empty(t, gone.RecoveredEmail, "a recovered account that is gone")
	require.ErrorIs(t, missingErr, account.ErrUserNotFound)
}

func TestRecoveryNoticeAdministrators(t *testing.T) {
	// Given two active administrators, a deactivated one, and a member.
	db, conn := setupStorage(t)
	var admins []int64
	for _, email := range []string{"first@example.com", "gone@example.com", "second@example.com"} {
		id := verificationUser(t, conn, email)
		_, err := conn.Exec(t.Context(), "UPDATE users SET role = 'admin' WHERE id = $1", id)
		require.NoError(t, err)
		admins = append(admins, id)
	}
	_, err := conn.Exec(t.Context(), "UPDATE users SET deactivated_at = now() WHERE id = $1", admins[1])
	require.NoError(t, err)
	verificationUser(t, conn, "member@example.com")

	ids, err := db.ListActiveAdministrators(t.Context())
	require.NoError(t, err)

	assert.Equal(t, []int64{admins[0], admins[2]}, ids)
}
