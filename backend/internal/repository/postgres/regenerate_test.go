package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/encryption"
	"inbok/internal/httpapi"
	"inbok/internal/passwordhash"
	"inbok/internal/repository/postgres/sqlc"
	"inbok/internal/totp"
)

func newRegeneration(t *testing.T) (*confirmationFixture, *totp.Backups) {
	t.Helper()
	f := newConfirmation(t)
	_, err := f.conn.Exec(t.Context(), "UPDATE user_totp SET confirmed_at=$1 WHERE user_id=$2", f.now, f.state.UserID)
	require.NoError(t, err)
	f.factor, err = f.db.ReadTOTP(t.Context(), f.state.UserID)
	require.NoError(t, err)
	for _, code := range []string{"ABCDEFGHJK", "MNPQRSTUVW"} {
		require.NoError(t, sqlc.New(f.conn).InsertTOTPBackupCode(t.Context(), sqlc.InsertTOTPBackupCodeParams{UserID: f.state.UserID, CodeHash: passwordhash.Hash(code)}))
	}
	ring, err := encryption.New("1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	require.NoError(t, err)
	clock := func() time.Time { return f.now }
	service := totp.NewBackups(f.db, ring, totp.NewVerifier(clock), totp.NewThrottle(f.db, clock, slog.New(slog.DiscardHandler)))
	return f, service
}

func TestRegeneratePersistence(t *testing.T) {
	f, service := newRegeneration(t)
	queries := sqlc.New(f.conn)
	old, err := queries.ReadTOTPBackupCodes(t.Context(), f.state.UserID)
	require.NoError(t, err)
	foreign, err := queries.ReadTOTPBackupCodes(t.Context(), f.foreign.UserID)
	require.NoError(t, err)

	codes, err := service.Regenerate(t.Context(), f.state.UserID, "287082")

	require.NoError(t, err)
	require.Len(t, codes, 10)
	rows, err := queries.ReadTOTPBackupCodes(t.Context(), f.state.UserID)
	require.NoError(t, err)
	require.Len(t, rows, 10)
	for i, row := range rows {
		require.NoError(t, totp.CheckBackupCode(codes[i], row.CodeHash))
		for _, previous := range old {
			assert.NotEqual(t, previous.ID, row.ID)
		}
	}
	factor, err := f.db.ReadTOTP(t.Context(), f.state.UserID)
	require.NoError(t, err)
	require.NotNil(t, factor)
	assert.Equal(t, int64(1), factor.LastStep)
	assert.Equal(t, f.factor.Ciphertext, factor.Ciphertext)
	assert.Equal(t, f.factor.ConfirmedAt, factor.ConfirmedAt)
	remaining, err := queries.ReadTOTPBackupCodes(t.Context(), f.foreign.UserID)
	require.NoError(t, err)
	assert.Equal(t, foreign, remaining)
	assert.Equal(t, f.state, readSession(t, f.db, f.state.TokenHash))
	assert.Equal(t, f.other, readSession(t, f.db, f.other.TokenHash))
	assert.Equal(t, f.foreign, readSession(t, f.db, f.foreign.TokenHash))
	attempts, err := queries.ListTOTPAttempts(t.Context(), f.state.UserID)
	require.NoError(t, err)
	assert.Len(t, attempts, len(f.attemptIDs), "success preserves earlier failures")

	// The accepted step cannot regenerate a second set, even with another request.
	result, err := service.Regenerate(t.Context(), f.state.UserID, "287082")
	require.ErrorIs(t, err, totp.ErrInvalidCode)
	assert.Nil(t, result)
	unchanged, err := queries.ReadTOTPBackupCodes(t.Context(), f.state.UserID)
	require.NoError(t, err)
	assert.Equal(t, rows, unchanged)
}

func TestRegenerateRollback(t *testing.T) {
	for _, c := range []struct{ name, trigger string }{
		{"insert", "CREATE TRIGGER reject_write BEFORE INSERT ON totp_backup_codes FOR EACH ROW EXECUTE FUNCTION reject_regeneration()"},
		{"attempt cleanup", "CREATE TRIGGER reject_write BEFORE DELETE ON totp_attempts FOR EACH ROW EXECUTE FUNCTION reject_regeneration()"},
		{"commit", "CREATE CONSTRAINT TRIGGER reject_write AFTER INSERT ON totp_backup_codes DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_regeneration()"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, service := newRegeneration(t)
			queries := sqlc.New(f.conn)
			old, err := queries.ReadTOTPBackupCodes(t.Context(), f.state.UserID)
			require.NoError(t, err)
			_, err = f.conn.Exec(t.Context(), `CREATE FUNCTION reject_regeneration() RETURNS trigger LANGUAGE plpgsql AS
			$$ BEGIN RAISE EXCEPTION 'forced regeneration failure'; END $$;`)
			require.NoError(t, err)
			_, err = f.conn.Exec(t.Context(), c.trigger)
			require.NoError(t, err)

			codes, err := service.Regenerate(t.Context(), f.state.UserID, "287082")

			require.ErrorContains(t, err, "forced regeneration failure")
			assert.Nil(t, codes, "new plaintext must not escape an unsuccessful commit")
			rows, err := queries.ReadTOTPBackupCodes(t.Context(), f.state.UserID)
			require.NoError(t, err)
			assert.Equal(t, old, rows)
			factor, err := f.db.ReadTOTP(t.Context(), f.state.UserID)
			require.NoError(t, err)
			assert.Equal(t, f.factor, factor)
			attempts, err := queries.ListTOTPAttempts(t.Context(), f.state.UserID)
			require.NoError(t, err)
			assert.Len(t, attempts, len(f.attemptIDs)+1, "failed work retains its reserved attempt")
		})
	}
}

func TestRegenerateParallel(t *testing.T) {
	f, service := newRegeneration(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	type result struct {
		codes []string
		err   error
	}
	start, results := make(chan struct{}), make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			codes, err := service.Regenerate(ctx, f.state.UserID, "287082")
			results <- result{codes, err}
		}()
	}
	close(start)

	var accepted, rejected int
	var winner []string
	for range 2 {
		got := <-results
		if errors.Is(got.err, totp.ErrInvalidCode) {
			rejected++
			assert.Nil(t, got.codes)
			continue
		}
		require.NoError(t, got.err)
		accepted++
		winner = got.codes
	}
	assert.Equal(t, 1, accepted)
	assert.Equal(t, 1, rejected)
	rows, err := sqlc.New(f.conn).ReadTOTPBackupCodes(t.Context(), f.state.UserID)
	require.NoError(t, err)
	require.Len(t, rows, 10)
	require.Len(t, winner, 10)
	for i, row := range rows {
		require.NoError(t, totp.CheckBackupCode(winner[i], row.CodeHash))
	}
}

func TestRegenerateHTTP(t *testing.T) {
	f, backups := newRegeneration(t)
	api := httpapi.New(slog.New(slog.DiscardHandler), httpapi.Services{Session: f.sessions, Backups: backups})
	request := httptest.NewRequest(http.MethodPost, "/api/v0/totp/backup-codes", strings.NewReader(`{"code":"287082"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://example.com")
	request.AddCookie(&http.Cookie{Name: "__Host-inbok-session", Value: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"})
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	assert.Empty(t, response.Result().Cookies())
	var body struct {
		Data struct {
			BackupCodes []string `json:"backupCodes"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Len(t, body.Data.BackupCodes, 10)
	rows, err := sqlc.New(f.conn).ReadTOTPBackupCodes(t.Context(), f.state.UserID)
	require.NoError(t, err)
	require.Len(t, rows, 10)
	require.NoError(t, totp.CheckBackupCode(body.Data.BackupCodes[0], rows[0].CodeHash))
}
