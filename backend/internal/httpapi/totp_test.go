package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/session"
	"inbok/internal/totp"
)

type fakeTOTP struct {
	status  totp.Status
	started totp.Started
	err     error
	calls   int
	user    account.User
	userID  int64
}

func (f *fakeTOTP) Status(_ context.Context, userID int64) (totp.Status, error) {
	f.calls++
	f.userID = userID
	return f.status, f.err
}

func (f *fakeTOTP) Start(_ context.Context, user account.User) (totp.Started, error) {
	f.calls++
	f.user = user
	return f.started, f.err
}

type totpAuth int

const (
	totpAuthAnonymous totpAuth = iota
	totpAuthAuthenticated
)

func requestTOTP(api *API, method string, auth totpAuth) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "/api/v0/totp", nil)
	request.Header.Set("Origin", "http://"+request.Host)
	if auth == totpAuthAuthenticated {
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-secret"})
	}
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	return response
}

func totpSessions(role account.Role) *fakeSessions {
	return &fakeSessions{identity: session.Identity{
		User:   account.User{ID: 42, Email: "alice@example.com", Role: role},
		Access: session.AccessMember, TOTPRequired: role == account.RoleAdmin,
	}}
}

func TestStartTOTPEnrollment(t *testing.T) {
	for _, role := range []account.Role{account.RoleMember, account.RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			service := &fakeTOTP{started: totp.Started{Secret: "manual-secret", URI: "otpauth://test"}}
			sessions := totpSessions(role)
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			api := New(logger, Services{TOTP: service, Session: sessions})
			response := requestTOTP(api, http.MethodPost, totpAuthAuthenticated)
			require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
			assert.JSONEq(t, `{"data":{"secret":"manual-secret","uri":"otpauth://test"}}`, response.Body.String())
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			assert.Equal(t, sessions.identity.User, service.user)
			assert.Equal(t, 1, service.calls)
			assert.NotContains(t, logs.String(), service.started.Secret)
			assert.NotContains(t, logs.String(), service.started.URI)
		})
	}
}

func TestGetTOTPStatus(t *testing.T) {
	for _, state := range []totp.State{totp.StateOff, totp.StatePending, totp.StateOn} {
		t.Run(string(state), func(t *testing.T) {
			count := 0
			if state == totp.StateOn {
				count = 7
			}
			service := &fakeTOTP{status: totp.Status{State: state, BackupCodesLeft: count}}
			response := requestTOTP(New(discard, Services{TOTP: service, Session: totpSessions(account.RoleMember)}), http.MethodGet, totpAuthAuthenticated)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.JSONEq(t, fmt.Sprintf(`{"data":{"status":"%s","backupCodesLeft":%d}}`, state, count), response.Body.String())
			assert.Equal(t, int64(42), service.userID)
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		})
	}
}

func TestTOTPRequiresSession(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			service := &fakeTOTP{}
			response := requestTOTP(New(discard, Services{TOTP: service}), method, totpAuthAnonymous)
			require.Equal(t, http.StatusUnauthorized, response.Code)
			assert.Zero(t, service.calls)
			assert.NotContains(t, response.Body.String(), "secret")
		})
	}
}

func TestStartTOTPAlreadyOn(t *testing.T) {
	service := &fakeTOTP{err: totp.ErrAlreadyOn}
	response := requestTOTP(New(discard, Services{TOTP: service, Session: totpSessions(account.RoleMember)}), http.MethodPost, totpAuthAuthenticated)
	require.Equal(t, http.StatusConflict, response.Code)
	assert.Contains(t, response.Body.String(), `"type":"/api/v0/problems/totp-already-on"`)
	assert.NotContains(t, response.Body.String(), `"secret"`)
}

func TestTOTPUnexpectedFailure(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			service := &fakeTOTP{err: errors.New("private storage cause")}
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			response := requestTOTP(New(logger, Services{TOTP: service, Session: totpSessions(account.RoleMember)}), method, totpAuthAuthenticated)
			require.Equal(t, http.StatusInternalServerError, response.Code)
			assert.NotContains(t, response.Body.String(), "private storage cause")
			assert.Contains(t, logs.String(), "private storage cause")
		})
	}
}

func TestTOTPCrossOrigin(t *testing.T) {
	service := &fakeTOTP{}
	api := New(discard, Services{TOTP: service, Session: totpSessions(account.RoleMember)})
	request := httptest.NewRequest(http.MethodPost, "/api/v0/totp", nil)
	request.Header.Set("Origin", "https://untrusted.example")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-secret"})
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	require.Equal(t, http.StatusForbidden, response.Code)
	assert.Zero(t, service.calls)
}

type fakeEnrollment struct {
	result  session.Confirmed
	err     error
	current session.Authenticated
	code    string
	calls   int
}

func (f *fakeEnrollment) Confirm(_ context.Context, current session.Authenticated, code string) (session.Confirmed, error) {
	f.calls++
	f.current, f.code = current, code
	return f.result, f.err
}

func postTOTPConfirm(api *API, body, origin string, auth totpAuth) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v0/totp/confirm", strings.NewReader(body))
	request.Header.Set("Content-Type", jsonContentType)
	request.Header.Set("Origin", origin)
	if auth == totpAuthAuthenticated {
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "old-cookie-secret"})
	}
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	return response
}

func TestConfirmTOTPEnrollment(t *testing.T) {
	for _, role := range []account.Role{account.RoleMember, account.RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			sessions := totpSessions(role)
			sessions.reference = checkedReference(t)
			expires := time.Now().Add(2 * time.Hour)
			service := &fakeEnrollment{result: session.Confirmed{
				Session: session.Issued{Secret: "replacement-cookie-secret", State: session.State{ExpiresAt: expires}},
				BackupCodes: []string{
					"ABCDE-FGHJK", "MNPQR-STUVW", "XYZ23-45678", "9ABCD-EFGHJ", "KMNPQ-RSTUV",
					"WXYZ2-34567", "89ABC-DEFGH", "JKMNP-QRSTU", "VWXYZ-23456", "789AB-CDEFG",
				},
			}}
			var logs bytes.Buffer
			api := New(slog.New(slog.NewTextHandler(&logs, nil)), Services{Session: sessions, Enrollment: service})

			response := postTOTPConfirm(api, `{"code":"287082"}`, "http://example.com", totpAuthAuthenticated)

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var body struct {
				Data totpConfirmation `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			assert.Equal(t, service.result.BackupCodes, body.Data.BackupCodes)
			assert.Equal(t, session.Authenticated{Identity: sessions.identity, Session: sessions.reference}, service.current)
			assert.Equal(t, "287082", service.code)
			assert.Equal(t, 1, service.calls)
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			cookies := response.Result().Cookies()
			require.Len(t, cookies, 1)
			cookie := cookies[0]
			assert.Equal(t, sessionCookieName, cookie.Name)
			assert.Equal(t, service.result.Session.Secret, cookie.Value)
			assert.Equal(t, "/", cookie.Path)
			assert.Empty(t, cookie.Domain)
			assert.True(t, cookie.Secure)
			assert.True(t, cookie.HttpOnly)
			assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
			assert.InDelta(t, int((2*time.Hour)/time.Second), cookie.MaxAge, 2)
			for _, secret := range []string{"287082", "old-cookie-secret", service.result.Session.Secret, "ABCDE-FGHJK", "MNPQR-STUVW"} {
				assert.NotContains(t, logs.String(), secret)
			}
			assert.NotContains(t, response.Body.String(), service.result.Session.Secret)
		})
	}
}

func TestConfirmTOTPFailures(t *testing.T) {
	for _, c := range []struct {
		name    string
		err     error
		status  int
		problem string
	}{
		{"invalid code", totp.ErrInvalidCode, http.StatusUnprocessableEntity, "invalid-totp-code"},
		{"not pending", totp.ErrNotPending, http.StatusConflict, "totp-not-pending"},
		{"already on", totp.ErrAlreadyOn, http.StatusConflict, "totp-already-on"},
		{"throttled", totp.ErrThrottled, http.StatusTooManyRequests, "totp-throttled"},
		{"session revoked during confirmation", session.ErrUnauthenticated, http.StatusUnauthorized, "unauthenticated"},
		{"commit failed", errors.New("private commit failure"), http.StatusInternalServerError, "internal-error"},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeEnrollment{err: fmt.Errorf("confirmation: %w", c.err), result: session.Confirmed{
				Session: session.Issued{Secret: "must-not-issue"}, BackupCodes: []string{"must-not-reveal"},
			}}
			var logs bytes.Buffer
			api := New(slog.New(slog.NewTextHandler(&logs, nil)), Services{Session: totpSessions(account.RoleMember), Enrollment: service})

			response := postTOTPConfirm(api, `{"code":"287082"}`, "http://example.com", totpAuthAuthenticated)

			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), `"type":"/api/v0/problems/`+c.problem+`"`)
			assert.Empty(t, response.Result().Cookies())
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			for _, secret := range []string{"287082", "must-not-issue", "must-not-reveal"} {
				assert.NotContains(t, response.Body.String(), secret)
				assert.NotContains(t, logs.String(), secret)
			}
			assert.NotContains(t, response.Body.String(), "private commit failure")
			if c.err == totp.ErrInvalidCode {
				assert.Contains(t, response.Body.String(), `"location":"body.code"`)
			}
		})
	}
}

func TestConfirmTOTPRequestGate(t *testing.T) {
	for _, c := range []struct {
		name, body, origin string
		auth               totpAuth
		status             int
	}{
		{"anonymous before parsing", `{`, "http://example.com", totpAuthAnonymous, http.StatusUnauthorized},
		{"cross origin", `{"code":"287082"}`, "https://untrusted.example", totpAuthAuthenticated, http.StatusForbidden},
		{"missing body", ``, "http://example.com", totpAuthAuthenticated, http.StatusBadRequest},
		{"missing code", `{}`, "http://example.com", totpAuthAuthenticated, http.StatusUnprocessableEntity},
		{"wrong type", `{"code":287082}`, "http://example.com", totpAuthAuthenticated, http.StatusUnprocessableEntity},
		{"too long", `{"code":"1234567"}`, "http://example.com", totpAuthAuthenticated, http.StatusUnprocessableEntity},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeEnrollment{}
			api := New(discard, Services{Session: totpSessions(account.RoleMember), Enrollment: service})

			response := postTOTPConfirm(api, c.body, c.origin, c.auth)

			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Zero(t, service.calls)
			assert.NotContains(t, response.Body.String(), "287082")
		})
	}
}

func TestConfirmTOTPOpenAPI(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()
	path := doc.Paths["/totp/confirm"]
	require.NotNil(t, path)
	op := path.Post
	require.NotNil(t, op)
	assert.Equal(t, "confirmTOTPEnrollment", op.OperationID)
	assert.Equal(t, session.AccessMember, op.Metadata[accessKey])
	assert.Equal(t, []map[string][]string{{sessionSecuritySchemeName: {}}}, op.Security)
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusTooManyRequests} {
		require.NotNil(t, op.Responses[strconv.Itoa(status)])
	}
	require.NotNil(t, op.Responses["200"].Headers["Set-Cookie"])
	input := doc.Components.Schemas.Map()["TotpConfirmationCode"]
	require.NotNil(t, input)
	require.NotNil(t, input.Properties["code"])
	assert.True(t, input.Properties["code"].WriteOnly)
	result := doc.Components.Schemas.Map()["TotpConfirmation"]
	require.NotNil(t, result)
	codes := result.Properties["backupCodes"]
	require.NotNil(t, codes)
	assert.False(t, codes.Nullable)
	require.NotNil(t, codes.MinItems)
	require.NotNil(t, codes.MaxItems)
	assert.Equal(t, 10, *codes.MinItems)
	assert.Equal(t, 10, *codes.MaxItems)
}

// ========================
// Backup-code regeneration
// ========================

type fakeBackups struct {
	codes  []string
	err    error
	calls  int
	userID int64
	code   string
}

func (f *fakeBackups) Regenerate(_ context.Context, userID int64, code string) ([]string, error) {
	f.calls++
	f.userID, f.code = userID, code
	return f.codes, f.err
}

func postBackupRegeneration(api *API, body, origin string, auth totpAuth) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v0/totp/backup-codes", strings.NewReader(body))
	request.Header.Set("Content-Type", jsonContentType)
	request.Header.Set("Origin", origin)
	if auth == totpAuthAuthenticated {
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-secret"})
	}

	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	return response
}

func TestRegenerateBackupCodes(t *testing.T) {
	for _, role := range []account.Role{account.RoleMember, account.RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			service := &fakeBackups{codes: []string{
				"ABCDE-FGHJK", "MNPQR-STUVW", "XYZ23-45678", "9ABCD-EFGHJ", "KMNPQ-RSTUV",
				"WXYZ2-34567", "89ABC-DEFGH", "JKMNP-QRSTU", "VWXYZ-23456", "789AB-CDEFG",
			}}
			sessions := totpSessions(role)
			if role == account.RoleAdmin {
				sessions.identity.Access = session.AccessAdmin
				sessions.identity.TOTPRequired = false
			}
			var logs bytes.Buffer
			api := New(slog.New(slog.NewTextHandler(&logs, nil)), Services{Session: sessions, Backups: service})

			response := postBackupRegeneration(api, `{"code":"012345"}`, "http://example.com", totpAuthAuthenticated)

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var body struct {
				Data totpBackupCodes `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			assert.Equal(t, service.codes, body.Data.BackupCodes)
			assert.Equal(t, int64(42), service.userID)
			assert.Equal(t, "012345", service.code)
			assert.Equal(t, 1, service.calls)
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			assert.Empty(t, response.Result().Cookies(), "regeneration does not replace or clear sessions")
			assert.NotContains(t, response.Body.String(), service.code)
			for _, secret := range append([]string{"012345", "session-secret"}, service.codes...) {
				assert.NotContains(t, logs.String(), secret)
			}
		})
	}
}

func TestRegenerateProblems(t *testing.T) {
	for _, c := range []struct {
		name    string
		err     error
		status  int
		problem string
	}{
		{"not on", totp.ErrNotOn, http.StatusConflict, "totp-not-on"},
		{"invalid code", totp.ErrInvalidCode, http.StatusUnprocessableEntity, "invalid-totp-code"},
		{"throttled", totp.ErrThrottled, http.StatusTooManyRequests, "totp-throttled"},
		{"deleted user", account.ErrUserNotFound, http.StatusUnauthorized, "unauthenticated"},
		{"commit failure", errors.New("private storage cause"), http.StatusInternalServerError, "internal-error"},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeBackups{codes: []string{"must-not-reveal"}, err: fmt.Errorf("regeneration: %w", c.err)}
			api := New(discard, Services{Session: totpSessions(account.RoleMember), Backups: service})

			response := postBackupRegeneration(api, `{"code":"012345"}`, "http://example.com", totpAuthAuthenticated)

			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), `"type":"/api/v0/problems/`+c.problem+`"`)
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			assert.Empty(t, response.Result().Cookies())
			for _, secret := range []string{"012345", "must-not-reveal", "private storage cause"} {
				assert.NotContains(t, response.Body.String(), secret)
			}
			if c.err == totp.ErrInvalidCode {
				assert.Contains(t, response.Body.String(), `"location":"body.code"`)
			}
		})
	}
}

func TestRegenerateRequestGate(t *testing.T) {
	for _, c := range []struct {
		name, body, origin string
		auth               totpAuth
		status             int
	}{
		{"anonymous before parsing", `{`, "http://example.com", totpAuthAnonymous, http.StatusUnauthorized},
		{"cross origin", `{"code":"012345"}`, "https://untrusted.example", totpAuthAuthenticated, http.StatusForbidden},
		{"missing code", `{}`, "http://example.com", totpAuthAuthenticated, http.StatusUnprocessableEntity},
		{"wrong type", `{"code":123456}`, "http://example.com", totpAuthAuthenticated, http.StatusUnprocessableEntity},
		{"backup code", `{"code":"ABCDE-FGHJK"}`, "http://example.com", totpAuthAuthenticated, http.StatusUnprocessableEntity},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeBackups{}
			api := New(discard, Services{Session: totpSessions(account.RoleMember), Backups: service})

			response := postBackupRegeneration(api, c.body, c.origin, c.auth)

			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Zero(t, service.calls)
		})
	}
}

func TestRegenerateOpenAPI(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()
	path := doc.Paths["/totp/backup-codes"]
	require.NotNil(t, path)
	op := path.Post
	require.NotNil(t, op)
	assert.Equal(t, "regenerateTOTPBackupCodes", op.OperationID)
	assert.Equal(t, session.AccessMember, op.Metadata[accessKey])
	assert.Equal(t, []map[string][]string{{sessionSecuritySchemeName: {}}}, op.Security)
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusTooManyRequests} {
		require.NotNil(t, op.Responses[strconv.Itoa(status)])
	}
	input := doc.Components.Schemas.Map()["TotpRegenerationCode"]
	require.NotNil(t, input)
	require.NotNil(t, input.Properties["code"])
	assert.True(t, input.Properties["code"].WriteOnly)
	result := doc.Components.Schemas.Map()["TotpBackupCodes"]
	require.NotNil(t, result)
	codes := result.Properties["backupCodes"]
	require.NotNil(t, codes)
	assert.False(t, codes.Nullable)
	require.NotNil(t, codes.MinItems)
	require.NotNil(t, codes.MaxItems)
	assert.Equal(t, 10, *codes.MinItems)
	assert.Equal(t, 10, *codes.MaxItems)
}

// ========================
// TOTP turn-off
// ========================

type fakeDisabler struct {
	err    error
	calls  int
	userID int64
	code   string
}

func (f *fakeDisabler) Disable(_ context.Context, userID int64, code string) error {
	f.calls++
	f.userID, f.code = userID, code
	return f.err
}

func postDisableTOTP(api *API, body, origin string, auth totpAuth) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v0/totp/disable", strings.NewReader(body))
	request.Header.Set("Content-Type", jsonContentType)
	request.Header.Set("Origin", origin)
	if auth == totpAuthAuthenticated {
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-secret"})
	}

	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	return response
}

func TestDisableTOTP(t *testing.T) {
	for _, code := range []string{"012345", "abcde-fghjk"} {
		t.Run(code, func(t *testing.T) {
			service := &fakeDisabler{}
			var logs bytes.Buffer
			api := New(slog.New(slog.NewTextHandler(&logs, nil)), Services{Session: totpSessions(account.RoleMember), Disabler: service})

			response := postDisableTOTP(api, `{"code":"`+code+`"}`, "http://example.com", totpAuthAuthenticated)

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.JSONEq(t, `{"data":{"status":"off","backupCodesLeft":0}}`, response.Body.String())
			assert.Equal(t, int64(42), service.userID)
			assert.Equal(t, code, service.code)
			assert.Equal(t, 1, service.calls)
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			assert.Empty(t, response.Result().Cookies())
			assert.NotContains(t, response.Body.String(), code)
			assert.NotContains(t, logs.String(), code)
			assert.NotContains(t, logs.String(), "session-secret")
		})
	}
}

func TestDisableTOTPProblems(t *testing.T) {
	for _, c := range []struct {
		name    string
		err     error
		status  int
		problem string
	}{
		{"admin", totp.ErrDisableForbidden, http.StatusForbidden, "totp-disable-forbidden"},
		{"not on", totp.ErrNotOn, http.StatusConflict, "totp-not-on"},
		{"invalid proof", totp.ErrInvalidCode, http.StatusUnprocessableEntity, "invalid-totp-proof"},
		{"throttled", totp.ErrThrottled, http.StatusTooManyRequests, "totp-throttled"},
		{"deleted user", account.ErrUserNotFound, http.StatusUnauthorized, "unauthenticated"},
		{"commit failure", errors.New("private turn-off cause"), http.StatusInternalServerError, "internal-error"},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeDisabler{err: fmt.Errorf("turn off: %w", c.err)}
			sessions := totpSessions(account.RoleMember)
			if c.err == totp.ErrDisableForbidden {
				sessions = totpSessions(account.RoleAdmin)
				sessions.identity.Access = session.AccessAdmin
				sessions.identity.TOTPRequired = false
			}
			api := New(discard, Services{Session: sessions, Disabler: service})

			response := postDisableTOTP(api, `{"code":"012345"}`, "http://example.com", totpAuthAuthenticated)

			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), `"type":"/api/v0/problems/`+c.problem+`"`)
			assert.Equal(t, 1, service.calls, "the service owns the locked role check")
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			assert.Empty(t, response.Result().Cookies())
			assert.NotContains(t, response.Body.String(), "private turn-off cause")
			assert.NotContains(t, response.Body.String(), "012345")
			assert.NotContains(t, response.Body.String(), `"status":"off"`)
			if c.err == totp.ErrInvalidCode {
				assert.Contains(t, response.Body.String(), `"location":"body.code"`)
			}
		})
	}
}

func TestDisableTOTPRequestGate(t *testing.T) {
	for _, c := range []struct {
		name, body, origin string
		auth               totpAuth
		status             int
	}{
		{"anonymous before parsing", `{`, "http://example.com", totpAuthAnonymous, http.StatusUnauthorized},
		{"cross origin", `{"code":"012345"}`, "https://untrusted.example", totpAuthAuthenticated, http.StatusForbidden},
		{"missing code", `{}`, "http://example.com", totpAuthAuthenticated, http.StatusUnprocessableEntity},
		{"wrong type", `{"code":123456}`, "http://example.com", totpAuthAuthenticated, http.StatusUnprocessableEntity},
		{"oversized", `{"code":"` + strings.Repeat("A", 129) + `"}`, "http://example.com", totpAuthAuthenticated, http.StatusUnprocessableEntity},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeDisabler{}
			api := New(discard, Services{Session: totpSessions(account.RoleMember), Disabler: service})

			response := postDisableTOTP(api, c.body, c.origin, c.auth)

			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Zero(t, service.calls)
		})
	}
}

func TestDisableTOTPOpenAPI(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()
	path := doc.Paths["/totp/disable"]
	require.NotNil(t, path)
	op := path.Post
	require.NotNil(t, op)
	assert.Equal(t, "disableTOTP", op.OperationID)
	assert.Equal(t, session.AccessMember, op.Metadata[accessKey])
	assert.Equal(t, []map[string][]string{{sessionSecuritySchemeName: {}}}, op.Security)
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusTooManyRequests} {
		require.NotNil(t, op.Responses[strconv.Itoa(status)])
	}
	input := doc.Components.Schemas.Map()["TotpDisableCode"]
	require.NotNil(t, input)
	require.NotNil(t, input.Properties["code"])
	assert.True(t, input.Properties["code"].WriteOnly)
	assert.Nil(t, op.Responses["200"].Headers["Set-Cookie"])
}
