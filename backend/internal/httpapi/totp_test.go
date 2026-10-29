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
			service := &fakeTOTP{status: totp.Status{State: state}}
			response := requestTOTP(New(discard, Services{TOTP: service, Session: totpSessions(account.RoleMember)}), http.MethodGet, totpAuthAuthenticated)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.JSONEq(t, `{"data":{"status":"`+string(state)+`","backupCodesLeft":0}}`, response.Body.String())
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
