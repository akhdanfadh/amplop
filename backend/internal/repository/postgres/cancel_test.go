package postgres

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/account"
	"amplop/internal/httpapi"
	"amplop/internal/repository/postgres/sqlc"
	"amplop/internal/session"
	"amplop/internal/totp"
)

func TestCancelEnrollmentStorage(t *testing.T) {
	f := newConfirmation(t)
	service := newTOTPService(t, f.db, time.Now)
	queries := sqlc.New(f.conn)
	foreign, err := f.db.ReadTOTP(t.Context(), f.foreign.UserID)
	require.NoError(t, err)
	foreignCodes, err := queries.ReadTOTPBackupCodes(t.Context(), f.foreign.UserID)
	require.NoError(t, err)

	// Repeating cancellation is harmless, including for an admin awaiting
	// setup, and removes the waiting codes.
	for range 2 {
		require.NoError(t, service.Cancel(t.Context(), f.state.UserID))
		status, err := service.Status(t.Context(), f.state.UserID)
		require.NoError(t, err)
		assert.Equal(t, totp.Status{State: totp.StateOff}, status)
		assert.Nil(t, readWaitingCodes(t, f.conn, f.state.UserID))
	}

	other, err := f.db.ReadTOTP(t.Context(), f.foreign.UserID)
	require.NoError(t, err)
	assert.Equal(t, foreign, other)
	otherCodes, err := queries.ReadTOTPBackupCodes(t.Context(), f.foreign.UserID)
	require.NoError(t, err)
	assert.Equal(t, foreignCodes, otherCodes)
	assert.Equal(t, f.state, readSession(t, f.db, f.state.TokenHash))
	assert.Equal(t, f.other, readSession(t, f.db, f.other.TokenHash))
	assert.Equal(t, f.foreign, readSession(t, f.db, f.foreign.TokenHash))
	attempts, err := queries.ListTOTPAttempts(t.Context(), f.state.UserID)
	require.NoError(t, err)
	assert.Len(t, attempts, len(f.attemptIDs), "cancellation neither verifies codes nor resets throttling")
}

func TestCancelConfirmedEnrollment(t *testing.T) {
	f := newConfirmation(t)
	_, err := f.service.Finish(t.Context(), f.current, f.codes.Token, session.Device{})
	require.NoError(t, err)
	before, err := f.db.ReadTOTP(t.Context(), f.state.UserID)
	require.NoError(t, err)
	queries := sqlc.New(f.conn)
	codes, err := queries.ReadTOTPBackupCodes(t.Context(), f.state.UserID)
	require.NoError(t, err)
	require.Len(t, codes, 10)

	err = newTOTPService(t, f.db, time.Now).Cancel(t.Context(), f.state.UserID)

	require.ErrorIs(t, err, totp.ErrAlreadyOn)
	after, err := f.db.ReadTOTP(t.Context(), f.state.UserID)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	afterCodes, err := queries.ReadTOTPBackupCodes(t.Context(), f.state.UserID)
	require.NoError(t, err)
	assert.Equal(t, codes, afterCodes)
}

func TestCancelEnrollmentRollback(t *testing.T) {
	t.Run("callback", func(t *testing.T) {
		f := newConfirmation(t)
		rejected := errors.New("cancel rejected")

		err := f.db.CancelTOTP(t.Context(), f.state.UserID, func(*totp.Factor) error { return rejected })

		require.ErrorIs(t, err, rejected)
		assertPendingConfirmation(t, f)
	})

	t.Run("commit", func(t *testing.T) {
		f := newConfirmation(t)
		_, err := f.conn.Exec(t.Context(), `CREATE FUNCTION reject_cancel() RETURNS trigger LANGUAGE plpgsql AS
 $$ BEGIN RAISE EXCEPTION 'forced cancellation failure'; END $$;
 CREATE CONSTRAINT TRIGGER reject_cancel AFTER DELETE ON user_totp
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_cancel();`)
		require.NoError(t, err)

		err = newTOTPService(t, f.db, time.Now).Cancel(t.Context(), f.state.UserID)

		require.ErrorContains(t, err, "forced cancellation failure")
		assertPendingConfirmation(t, f)
	})
}

func TestCancelUnknownUser(t *testing.T) {
	db, _ := setupStorage(t)
	err := db.CancelTOTP(t.Context(), 123, func(*totp.Factor) error {
		t.Fatal("unknown owner must not reach the check")
		return nil
	})
	require.ErrorIs(t, err, account.ErrUserNotFound)
}

func TestCancelSeesConfirmation(t *testing.T) {
	f := newConfirmation(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tx, err := f.conn.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(t.Context())
	_, err = tx.Exec(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", f.state.UserID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, "UPDATE user_totp SET confirmed_at=now(), last_step=1 WHERE user_id=$1", f.state.UserID)
	require.NoError(t, err)

	results := make(chan error, 1)
	service := newTOTPService(t, f.db, time.Now)
	go func() { results <- service.Cancel(ctx, f.state.UserID) }()
	// Cancellation must wait for confirmation's owner lock, then read its commit.
	require.Eventually(t, func() bool {
		var blocked bool
		err := f.db.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%LockTOTPUser%')").Scan(&blocked)
		return err == nil && blocked
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, tx.Commit(ctx))
	require.ErrorIs(t, <-results, totp.ErrAlreadyOn)
	factor, err := f.db.ReadTOTP(ctx, f.state.UserID)
	require.NoError(t, err)
	require.NotNil(t, factor)
	assert.NotNil(t, factor.ConfirmedAt)
}

func TestCancelBeforeConfirmation(t *testing.T) {
	f := newConfirmation(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ready, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	canceled, confirmed := make(chan error, 1), make(chan error, 1)
	go func() {
		canceled <- f.db.CancelTOTP(ctx, f.state.UserID, func(current *totp.Factor) error {
			if current == nil || current.ConfirmedAt != nil {
				return errors.New("expected pending enrollment")
			}
			close(ready)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		require.FailNow(t, "cancellation did not acquire the owner lock")
	}
	go func() {
		_, err := f.service.Finish(ctx, f.current, f.codes.Token, session.Device{})
		confirmed <- err
	}()
	require.Eventually(t, func() bool {
		var blocked bool
		err := f.db.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%LockTOTPUser%')").Scan(&blocked)
		return err == nil && blocked
	}, 3*time.Second, 10*time.Millisecond)
	unblock()

	require.NoError(t, <-canceled)
	require.ErrorIs(t, <-confirmed, totp.ErrNotPending)
	factor, err := f.db.ReadTOTP(ctx, f.state.UserID)
	require.NoError(t, err)
	assert.Nil(t, factor)
	assert.Equal(t, f.state, readSession(t, f.db, f.state.TokenHash))
}

func TestCancelEnrollmentHTTP(t *testing.T) {
	for _, role := range []account.Role{account.RoleMember, account.RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			f := newConfirmation(t)
			_, err := f.conn.Exec(t.Context(), "UPDATE users SET role=$1 WHERE id=$2", string(role), f.state.UserID)
			require.NoError(t, err)
			api := httpapi.New(slog.New(slog.DiscardHandler), httpapi.Services{
				Session: f.sessions, TOTP: newTOTPService(t, f.db, time.Now),
			})
			cookie := &http.Cookie{Name: "__Host-amplop-session", Value: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}

			for range 2 {
				request := httptest.NewRequest(http.MethodDelete, "/api/v0/totp/enrollment", nil)
				request.Header.Set("Origin", "http://example.com")
				request.AddCookie(cookie)
				response := httptest.NewRecorder()
				api.Handler().ServeHTTP(response, request)

				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				assert.JSONEq(t, `{"data":{"status":"off","backupCodesLeft":0,"backupCodesWaiting":false}}`, response.Body.String())
				assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
				assert.Empty(t, response.Result().Cookies())
			}

			// The same cookie still reads status, now without a pending enrollment.
			request := httptest.NewRequest(http.MethodGet, "/api/v0/totp", nil)
			request.AddCookie(cookie)
			response := httptest.NewRecorder()
			api.Handler().ServeHTTP(response, request)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.JSONEq(t, `{"data":{"status":"off","backupCodesLeft":0,"backupCodesWaiting":false}}`, response.Body.String())
			assert.Equal(t, f.state, readSession(t, f.db, f.state.TokenHash))
			assert.Equal(t, f.other, readSession(t, f.db, f.other.TokenHash))
		})
	}
}

func TestCancelConfirmedHTTP(t *testing.T) {
	f := newConfirmation(t)
	confirmed, err := f.service.Finish(t.Context(), f.current, f.codes.Token, session.Device{})
	require.NoError(t, err)
	queries := sqlc.New(f.conn)
	before, err := f.db.ReadTOTP(t.Context(), f.state.UserID)
	require.NoError(t, err)
	codes, err := queries.ReadTOTPBackupCodes(t.Context(), f.state.UserID)
	require.NoError(t, err)
	require.Len(t, codes, 10)
	api := httpapi.New(slog.New(slog.DiscardHandler), httpapi.Services{
		Session: f.sessions, TOTP: newTOTPService(t, f.db, time.Now),
	})
	request := httptest.NewRequest(http.MethodDelete, "/api/v0/totp/enrollment", nil)
	request.Header.Set("Origin", "http://example.com")
	request.AddCookie(&http.Cookie{Name: "__Host-amplop-session", Value: confirmed.Secret})
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"type":"/api/v0/problems/totp-already-on"`)
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	assert.Empty(t, response.Result().Cookies())
	after, err := f.db.ReadTOTP(t.Context(), f.state.UserID)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	afterCodes, err := queries.ReadTOTPBackupCodes(t.Context(), f.state.UserID)
	require.NoError(t, err)
	assert.Equal(t, codes, afterCodes)
	assert.Equal(t, confirmed.State, readSession(t, f.db, confirmed.State.TokenHash))
}
