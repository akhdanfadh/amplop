package postgres

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/encryption"
	"inbok/internal/repository/postgres/sqlc"
	"inbok/internal/totp"
)

func newTurnOff(t *testing.T) (*confirmationFixture, *totp.Disabler) {
	t.Helper()
	f, _ := newRegeneration(t)
	_, err := f.conn.Exec(t.Context(), "UPDATE users SET role='member' WHERE id=$1", f.state.UserID)
	require.NoError(t, err)
	ring, err := encryption.New("1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	require.NoError(t, err)
	clock := func() time.Time { return f.now }
	service := totp.NewDisabler(f.db, ring, totp.NewVerifier(clock), totp.NewThrottle(f.db, clock, slog.New(slog.DiscardHandler)))
	return f, service
}

func TestDisablePersistence(t *testing.T) {
	for _, code := range []string{"287082", "abcde-fghjk"} {
		t.Run(code, func(t *testing.T) {
			f, service := newTurnOff(t)
			queries := sqlc.New(f.conn)
			foreign, err := queries.ReadTOTPBackupCodes(t.Context(), f.foreign.UserID)
			require.NoError(t, err)

			err = service.Disable(t.Context(), f.state.UserID, code)

			require.NoError(t, err)
			factor, err := f.db.ReadTOTP(t.Context(), f.state.UserID)
			require.NoError(t, err)
			assert.Nil(t, factor)
			rows, err := queries.ReadTOTPBackupCodes(t.Context(), f.state.UserID)
			require.NoError(t, err)
			assert.Empty(t, rows)
			other, err := queries.ReadTOTPBackupCodes(t.Context(), f.foreign.UserID)
			require.NoError(t, err)
			assert.Equal(t, foreign, other)
			assert.Equal(t, f.state, readSession(t, f.db, f.state.TokenHash))
			assert.Equal(t, f.other, readSession(t, f.db, f.other.TokenHash))
			assert.Equal(t, f.foreign, readSession(t, f.db, f.foreign.TokenHash))
			attempts, err := queries.ListTOTPAttempts(t.Context(), f.state.UserID)
			require.NoError(t, err)
			assert.Len(t, attempts, len(f.attemptIDs), "keep earlier failures")
		})
	}
}

func TestDisableAdmin(t *testing.T) {
	f, service := newTurnOff(t)
	_, err := f.conn.Exec(t.Context(), "UPDATE users SET role='admin' WHERE id=$1", f.state.UserID)
	require.NoError(t, err)

	err = service.Disable(t.Context(), f.state.UserID, "287082")

	require.ErrorIs(t, err, totp.ErrDisableForbidden)
	factor, err := f.db.ReadTOTP(t.Context(), f.state.UserID)
	require.NoError(t, err)
	assert.Equal(t, f.factor, factor)
}

func TestDisableRollback(t *testing.T) {
	for _, c := range []struct{ name, trigger string }{
		{"backup deletion", "CREATE TRIGGER reject_write BEFORE DELETE ON totp_backup_codes FOR EACH ROW EXECUTE FUNCTION reject_turnoff()"},
		{"attempt cleanup", "CREATE TRIGGER reject_write BEFORE DELETE ON totp_attempts FOR EACH ROW EXECUTE FUNCTION reject_turnoff()"},
		{"commit", "CREATE CONSTRAINT TRIGGER reject_write AFTER DELETE ON user_totp DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_turnoff()"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, service := newTurnOff(t)
			queries := sqlc.New(f.conn)
			old, err := queries.ReadTOTPBackupCodes(t.Context(), f.state.UserID)
			require.NoError(t, err)
			_, err = f.conn.Exec(t.Context(), `CREATE FUNCTION reject_turnoff() RETURNS trigger LANGUAGE plpgsql AS
   $$ BEGIN RAISE EXCEPTION 'forced turn-off failure'; END $$;`)
			require.NoError(t, err)
			_, err = f.conn.Exec(t.Context(), c.trigger)
			require.NoError(t, err)

			err = service.Disable(t.Context(), f.state.UserID, "abcde-fghjk")

			require.ErrorContains(t, err, "forced turn-off failure")
			factor, err := f.db.ReadTOTP(t.Context(), f.state.UserID)
			require.NoError(t, err)
			assert.Equal(t, f.factor, factor)
			rows, err := queries.ReadTOTPBackupCodes(t.Context(), f.state.UserID)
			require.NoError(t, err)
			assert.Equal(t, old, rows, "rollback restores the matched backup code too")
			attempts, err := queries.ListTOTPAttempts(t.Context(), f.state.UserID)
			require.NoError(t, err)
			assert.Len(t, attempts, len(f.attemptIDs)+1)
		})
	}
}

func TestDisableParallel(t *testing.T) {
	for _, code := range []string{"287082", "abcde-fghjk"} {
		t.Run(code, func(t *testing.T) {
			f, service := newTurnOff(t)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			start, results := make(chan struct{}), make(chan error, 2)
			for range 2 {
				go func() { <-start; results <- service.Disable(ctx, f.state.UserID, code) }()
			}
			close(start)

			var accepted, rejected int
			for range 2 {
				err := <-results
				if errors.Is(err, totp.ErrNotOn) {
					rejected++
					continue
				}
				require.NoError(t, err)
				accepted++
			}
			assert.Equal(t, 1, accepted)
			assert.Equal(t, 1, rejected)
		})
	}
}

func TestDisablePromotion(t *testing.T) {
	f, _ := newTurnOff(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tx, err := f.conn.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(t.Context())
	_, err = tx.Exec(ctx, "UPDATE users SET role='admin' WHERE id=$1", f.state.UserID)
	require.NoError(t, err)
	results := make(chan error, 1)
	go func() {
		results <- f.db.DisableTOTP(ctx, f.state.UserID, 0, func(state totp.DisableState) error {
			if state.Role != account.RoleAdmin {
				return errors.New("loaded stale member role")
			}
			return totp.ErrDisableForbidden
		})
	}()

	// The role read must wait, then see the promotion committed by this transaction.
	require.Eventually(t, func() bool {
		var blocked bool
		err := f.db.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%LockTOTPUserRole%')").Scan(&blocked)
		return err == nil && blocked
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, tx.Commit(ctx))
	require.ErrorIs(t, <-results, totp.ErrDisableForbidden)
	factor, err := f.db.ReadTOTP(t.Context(), f.state.UserID)
	require.NoError(t, err)
	assert.Equal(t, f.factor, factor)
}
