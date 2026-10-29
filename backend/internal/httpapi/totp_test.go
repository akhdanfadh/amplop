package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

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
