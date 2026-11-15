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

	"inbok/internal/httpapi"
	"inbok/internal/repository/postgres/sqlc"
	"inbok/internal/totp"
)

// newReplacement turns the fixture's factor on, with two active backup
// codes and no codes waiting.
func newReplacement(t *testing.T) (*confirmationFixture, *totp.Backups) {
	t.Helper()
	f := newConfirmation(t)
	_, err := f.conn.Exec(t.Context(), "UPDATE user_totp SET confirmed_at=$1, last_step=-1 WHERE user_id=$2", f.now, f.state.UserID)
	require.NoError(t, err)
	_, err = f.conn.Exec(t.Context(), "DELETE FROM totp_waiting_backup_codes WHERE user_id=$1", f.state.UserID)
	require.NoError(t, err)
	f.factor, err = f.db.ReadTOTP(t.Context(), f.state.UserID)
	require.NoError(t, err)
	for _, code := range []string{"ABCDEFGHJK", "MNPQRSTUVW"} {
		require.NoError(t, sqlc.New(f.conn).InsertTOTPBackupCode(t.Context(), sqlc.InsertTOTPBackupCodeParams{UserID: f.state.UserID, CodeHash: hashPassword(t, code)}))
	}
	clock := func() time.Time { return f.now }
	service := totp.NewBackups(f.db, testKeyring(t), totp.NewVerifier(clock), totp.NewThrottle(f.db, clock, slog.New(slog.DiscardHandler)), clock)
	return f, service
}

// TestReplacePersistence follows backup-code replacement through storage.
// Replacement has no storage file of its own: it runs through [DB.UpdateTOTP]
// (totp.go). This file groups the flow's tests, as enrollment_test.go does
// for finishing an enrollment.
func TestReplacePersistence(t *testing.T) {
	f, service := newReplacement(t)
	queries := sqlc.New(f.conn)
	old, err := queries.ReadTOTPBackupCodes(t.Context(), f.state.UserID)
	require.NoError(t, err)
	foreign, err := queries.ReadTOTPBackupCodes(t.Context(), f.foreign.UserID)
	require.NoError(t, err)

	codes, err := service.Replace(t.Context(), f.state.UserID, "287082")

	require.NoError(t, err)
	require.Len(t, codes.Codes, 10)
	waiting := readWaitingCodes(t, f.conn, f.state.UserID)
	require.NotNil(t, waiting)
	for i, hash := range waiting.Hashes {
		require.NoError(t, totp.VerifyBackupCode(t.Context(), codes.Codes[i], hash))
	}
	// The codes appear only in Replace's answer: no copy is stored.
	var copies int
	require.NoError(t, f.conn.QueryRow(t.Context(),
		"SELECT count(*) FROM totp_waiting_backup_codes WHERE user_id = $1 AND (key_id IS NOT NULL OR codes_ciphertext IS NOT NULL)",
		f.state.UserID).Scan(&copies))
	assert.Zero(t, copies)
	unchanged, err := queries.ReadTOTPBackupCodes(t.Context(), f.state.UserID)
	require.NoError(t, err)
	assert.Equal(t, old, unchanged, "the old codes work until finish")
	factor, err := f.db.ReadTOTP(t.Context(), f.state.UserID)
	require.NoError(t, err)
	require.NotNil(t, factor)
	assert.Equal(t, int64(1), factor.LastStep)
	assert.Equal(t, f.factor.Ciphertext, factor.Ciphertext)
	assert.Equal(t, f.factor.ConfirmedAt, factor.ConfirmedAt)
	attempts, err := queries.ListTOTPAttempts(t.Context(), f.state.UserID)
	require.NoError(t, err)
	assert.Len(t, attempts, len(f.attemptIDs), "success preserves earlier failures")

	// The accepted step cannot make a second set.
	result, err := service.Replace(t.Context(), f.state.UserID, "287082")
	require.ErrorIs(t, err, totp.ErrInvalidCode)
	assert.Empty(t, result)

	active, err := service.Finish(t.Context(), f.state.UserID, codes.Token)

	require.NoError(t, err)
	assert.Equal(t, 10, active)
	rows, err := queries.ReadTOTPBackupCodes(t.Context(), f.state.UserID)
	require.NoError(t, err)
	require.Len(t, rows, 10)
	for i, row := range rows {
		require.NoError(t, totp.VerifyBackupCode(t.Context(), codes.Codes[i], row.CodeHash))
		for _, previous := range old {
			assert.NotEqual(t, previous.ID, row.ID)
		}
	}
	assert.Nil(t, readWaitingCodes(t, f.conn, f.state.UserID))
	remaining, err := queries.ReadTOTPBackupCodes(t.Context(), f.foreign.UserID)
	require.NoError(t, err)
	assert.Equal(t, foreign, remaining)
	assert.Equal(t, f.state, readSession(t, f.db, f.state.TokenHash))
	assert.Equal(t, f.other, readSession(t, f.db, f.other.TokenHash))
	assert.Equal(t, f.foreign, readSession(t, f.db, f.foreign.TokenHash))

	_, err = service.Finish(t.Context(), f.state.UserID, codes.Token)
	require.ErrorIs(t, err, totp.ErrNotWaiting, "a token works once")
}

func TestReplaceRollback(t *testing.T) {
	for _, c := range []struct{ name, trigger string }{
		{"insert", "CREATE TRIGGER reject_write BEFORE INSERT ON totp_waiting_backup_codes FOR EACH ROW EXECUTE FUNCTION reject_replacement()"},
		{"attempt cleanup", "CREATE TRIGGER reject_write BEFORE DELETE ON totp_attempts FOR EACH ROW EXECUTE FUNCTION reject_replacement()"},
		{"commit", "CREATE CONSTRAINT TRIGGER reject_write AFTER INSERT ON totp_waiting_backup_codes DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_replacement()"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, service := newReplacement(t)
			queries := sqlc.New(f.conn)
			old, err := queries.ReadTOTPBackupCodes(t.Context(), f.state.UserID)
			require.NoError(t, err)
			_, err = f.conn.Exec(t.Context(), `CREATE FUNCTION reject_replacement() RETURNS trigger LANGUAGE plpgsql AS
			$$ BEGIN RAISE EXCEPTION 'forced replacement failure'; END $$;`)
			require.NoError(t, err)
			_, err = f.conn.Exec(t.Context(), c.trigger)
			require.NoError(t, err)

			codes, err := service.Replace(t.Context(), f.state.UserID, "287082")

			require.ErrorContains(t, err, "forced replacement failure")
			assert.Empty(t, codes, "new plaintext must not escape an unsuccessful commit")
			rows, err := queries.ReadTOTPBackupCodes(t.Context(), f.state.UserID)
			require.NoError(t, err)
			assert.Equal(t, old, rows)
			assert.Nil(t, readWaitingCodes(t, f.conn, f.state.UserID))
			factor, err := f.db.ReadTOTP(t.Context(), f.state.UserID)
			require.NoError(t, err)
			assert.Equal(t, f.factor, factor)
			attempts, err := queries.ListTOTPAttempts(t.Context(), f.state.UserID)
			require.NoError(t, err)
			assert.Len(t, attempts, len(f.attemptIDs)+1, "failed work retains its reserved attempt")
		})
	}
}

func TestReplaceFinishRollback(t *testing.T) {
	f, service := newReplacement(t)
	queries := sqlc.New(f.conn)
	old, err := queries.ReadTOTPBackupCodes(t.Context(), f.state.UserID)
	require.NoError(t, err)
	codes, err := service.Replace(t.Context(), f.state.UserID, "287082")
	require.NoError(t, err)
	waiting := readWaitingCodes(t, f.conn, f.state.UserID)
	_, err = f.conn.Exec(t.Context(), `CREATE FUNCTION reject_finish() RETURNS trigger LANGUAGE plpgsql AS
	$$ BEGIN RAISE EXCEPTION 'forced finish failure'; END $$;
	CREATE CONSTRAINT TRIGGER reject_write AFTER INSERT ON totp_backup_codes
	DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_finish();`)
	require.NoError(t, err)

	_, err = service.Finish(t.Context(), f.state.UserID, codes.Token)

	require.ErrorContains(t, err, "forced finish failure")
	rows, err := queries.ReadTOTPBackupCodes(t.Context(), f.state.UserID)
	require.NoError(t, err)
	assert.Equal(t, old, rows)
	assert.Equal(t, waiting, readWaitingCodes(t, f.conn, f.state.UserID), "the new codes still wait")
}

func TestReplaceParallel(t *testing.T) {
	f, service := newReplacement(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	type result struct {
		codes totp.Codes
		err   error
	}
	start, results := make(chan struct{}), make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			codes, err := service.Replace(ctx, f.state.UserID, "287082")
			results <- result{codes, err}
		}()
	}
	close(start)

	var accepted, rejected int
	var winner totp.Codes
	for range 2 {
		got := <-results
		if errors.Is(got.err, totp.ErrInvalidCode) {
			rejected++
			assert.Empty(t, got.codes)
			continue
		}
		require.NoError(t, got.err)
		accepted++
		winner = got.codes
	}
	assert.Equal(t, 1, accepted)
	assert.Equal(t, 1, rejected)
	waiting := readWaitingCodes(t, f.conn, f.state.UserID)
	require.NotNil(t, waiting)
	require.NoError(t, totp.VerifyWaiting(waiting, winner.Token, f.now))
}

func TestReplaceHTTP(t *testing.T) {
	f, backups := newReplacement(t)
	api := httpapi.New(slog.New(slog.DiscardHandler), httpapi.Services{Session: f.sessions, Backups: backups})
	send := func(path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "http://example.com")
		request.AddCookie(&http.Cookie{Name: "__Host-inbok-session", Value: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"})
		response := httptest.NewRecorder()
		api.Handler().ServeHTTP(response, request)
		return response
	}

	response := send("/api/v0/totp/backup-codes", `{"code":"287082"}`)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	assert.Empty(t, response.Result().Cookies())
	var body struct {
		Data struct {
			BackupCodes []string `json:"backupCodes"`
			Token       string   `json:"token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Len(t, body.Data.BackupCodes, 10)

	response = send("/api/v0/totp/backup-codes/finish", `{"token":"`+body.Data.Token+`"}`)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"status":"on","backupCodesLeft":10,"backupCodesWaiting":false}}`, response.Body.String())
	rows, err := sqlc.New(f.conn).ReadTOTPBackupCodes(t.Context(), f.state.UserID)
	require.NoError(t, err)
	require.Len(t, rows, 10)
	require.NoError(t, totp.VerifyBackupCode(t.Context(), body.Data.BackupCodes[0], rows[0].CodeHash))
}
