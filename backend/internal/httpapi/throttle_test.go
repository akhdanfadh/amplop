package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/attemptlimit"
	"inbok/internal/emailverification"
	"inbok/internal/passwordreset"
	"inbok/internal/session"
	"inbok/internal/totp"
)

type fakeThrottle struct {
	beginErr  error
	deleteErr error
	addr      netip.Addr
	begun     int
	removed   []int64
}

func (f *fakeThrottle) BeginSetup(_ context.Context, addr netip.Addr) (int64, error) {
	f.begun++
	f.addr = addr
	return 42, f.beginErr
}

func (f *fakeThrottle) Succeed(_ context.Context, id int64) error {
	f.removed = append(f.removed, id)
	return f.deleteErr
}

func TestSetupThrottling(t *testing.T) {
	lost := errors.New("attempt storage unavailable")
	for _, c := range []struct {
		name      string
		beginErr  error
		setupErr  error
		deleteErr error
		code      int
		created   int
		removed   []int64
	}{
		{"success", nil, nil, nil, http.StatusCreated, 1, []int64{42}},
		{"invalid token", nil, account.ErrSetupTokenInvalid, nil, http.StatusForbidden, 1, nil},
		{"complete", nil, account.ErrSetupDone, nil, http.StatusConflict, 1, nil},
		{"unconfigured", nil, account.ErrSetupUnconfigured, nil, http.StatusConflict, 1, nil},
		{"throttled", session.ErrThrottled, nil, nil, http.StatusTooManyRequests, 0, nil},
		{"reserve failure", lost, nil, nil, http.StatusInternalServerError, 0, nil},
		{"setup storage failure", nil, lost, nil, http.StatusInternalServerError, 1, nil},
		{"remove failure", nil, nil, lost, http.StatusInternalServerError, 1, []int64{42}},
	} {
		t.Run(c.name, func(t *testing.T) {
			accounts := &fakeAccounts{setupErr: c.setupErr}
			limiter := &fakeThrottle{beginErr: c.beginErr, deleteErr: c.deleteErr}
			api := New(discard, Services{Account: accounts, Throttle: limiter, FirstAdminSignIn: &fakeFirstAdmin{}})
			require.NoError(t, api.SetTrustedProxies([]string{"10.0.0.0/8"}))
			request := httptest.NewRequest(http.MethodPost, "/api/v0/setup", strings.NewReader(setupBody(t, setupFields())))
			request.Header.Set("Origin", "http://"+request.Host)
			request.Header.Set("Content-Type", jsonContentType)
			request.Header.Set("X-Forwarded-For", "198.51.100.20")
			request.RemoteAddr = "10.1.2.3:1234"
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			require.Equal(t, c.code, response.Code, response.Body.String())
			assert.Equal(t, netip.MustParseAddr("198.51.100.20"), limiter.addr)
			assert.Len(t, accounts.created, c.created)
			assert.Equal(t, c.removed, limiter.removed)
			if c.code != http.StatusCreated {
				assert.Empty(t, response.Result().Cookies())
			}
			if c.beginErr == session.ErrThrottled {
				assert.Equal(t, "/api/v0/problems/sign-in-throttled", decodeJSON(t, response.Body.Bytes())["type"])
			}
		})
	}
}

func TestSetupValidationFirst(t *testing.T) {
	limiter := &fakeThrottle{beginErr: session.ErrThrottled}
	accounts := &fakeAccounts{}
	api := New(discard, Services{Account: accounts, Throttle: limiter, FirstAdminSignIn: &fakeFirstAdmin{}})
	request := httptest.NewRequest(http.MethodPost, "/api/v0/setup", strings.NewReader(`{"token":"private-token","email":"bad","password":"short"}`))
	request.Header.Set("Origin", "http://"+request.Host)
	request.Header.Set("Content-Type", jsonContentType)
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	require.Equal(t, http.StatusUnprocessableEntity, response.Code)
	assert.Zero(t, limiter.begun)
	assert.Empty(t, accounts.created)
}

// Every throttle's 429 says how long to wait, in whole seconds.
func TestRetryAfter(t *testing.T) {
	// refused returns err as a throttle reports it: 90 seconds and a
	// millisecond to wait, which rounds up to 91.
	refused := func(err error) error {
		return &attemptlimit.Error{Err: err, Wait: 90*time.Second + time.Millisecond}
	}
	for _, c := range []struct {
		name string
		send func() *httptest.ResponseRecorder
		typ  string
	}{
		{"setup", func() *httptest.ResponseRecorder {
			api := New(discard, Services{Account: &fakeAccounts{}, Throttle: &fakeThrottle{beginErr: refused(session.ErrThrottled)}})
			request := httptest.NewRequest(http.MethodPost, "/api/v0/setup", strings.NewReader(setupBody(t, setupFields())))
			request.Header.Set("Origin", "http://"+request.Host)
			request.Header.Set("Content-Type", jsonContentType)
			response := httptest.NewRecorder()
			api.Handler().ServeHTTP(response, request)
			return response
		}, "sign-in-throttled"},
		{"sign-in", func() *httptest.ResponseRecorder {
			return postSession(New(discard, Services{Session: &fakeSessions{err: refused(session.ErrThrottled)}}), signInHTTPBody)
		}, "sign-in-throttled"},
		{"code sign-in", func() *httptest.ResponseRecorder {
			return postCode(New(discard, Services{CodeSignIn: &fakeCodeSignIn{err: refused(totp.ErrThrottled)}}), `{"code":"012345"}`, "http://example.com")
		}, "totp-throttled"},
		{"TOTP setup code", func() *httptest.ResponseRecorder {
			api := New(discard, Services{Session: memberSessions(account.RoleMember), TOTP: &fakeTOTP{err: refused(totp.ErrThrottled)}})
			return postTOTP(api, "/enrollment/code", `{"code":"287082"}`, "http://example.com", totpAuthAuthenticated)
		}, "totp-throttled"},
		{"TOTP setup password", func() *httptest.ResponseRecorder {
			api := New(discard, Services{Session: memberSessions(account.RoleMember), TOTP: &fakeTOTP{err: refused(session.ErrThrottled)}})
			return postTOTP(api, "", `{"password":"pw"}`, "http://example.com", totpAuthAuthenticated)
		}, "sign-in-throttled"},
		{"backup codes", func() *httptest.ResponseRecorder {
			api := New(discard, Services{Session: memberSessions(account.RoleMember), Backups: &fakeBackups{err: refused(totp.ErrThrottled)}})
			return postTOTP(api, "/backup-codes", `{"code":"287082"}`, "http://example.com", totpAuthAuthenticated)
		}, "totp-throttled"},
		{"TOTP turn-off", func() *httptest.ResponseRecorder {
			api := New(discard, Services{Session: memberSessions(account.RoleMember), Disabler: &fakeDisabler{err: refused(totp.ErrThrottled)}})
			return postDisableTOTP(api, `{"code":"287082"}`, "http://example.com", totpAuthAuthenticated)
		}, "totp-throttled"},
		{"verification link", func() *httptest.ResponseRecorder {
			api := New(discard, Services{EmailVerification: &fakeEmailVerification{err: refused(emailverification.ErrThrottled)}, Session: memberSessions(account.RoleMember)})
			return sendSessionRequest(api, http.MethodPost, "/account/email-verification")
		}, "email-verification-throttled"},
		{"password reset link", func() *httptest.ResponseRecorder {
			api := New(discard, Services{PasswordResetRequests: &fakeResetRequests{err: refused(passwordreset.ErrThrottled)}})
			return postPublic(api, passwordResetLinksPath, `{"email":"member@example.org"}`)
		}, "password-reset-throttled"},
	} {
		t.Run(c.name, func(t *testing.T) {
			response := c.send()

			require.Equal(t, http.StatusTooManyRequests, response.Code, response.Body.String())
			assert.Equal(t, problemPrefix+c.typ, decodeJSON(t, response.Body.Bytes())["type"])
			assert.Equal(t, "91", response.Header().Get(retryAfterHeader))
		})
	}
}

// Retry-After rounds the wait up to whole seconds, never below 1.
func TestRetryAfterSeconds(t *testing.T) {
	for _, c := range []struct {
		wait time.Duration
		want int
	}{
		{time.Nanosecond, 1},
		{time.Second, 1},
		{time.Second + time.Nanosecond, 2},
		{15 * time.Minute, 900},
		{24*time.Hour - time.Second, 86399},
	} {
		t.Run(c.wait.String(), func(t *testing.T) {
			err := retryAfterProblem(requestProblem(http.StatusTooManyRequests), &attemptlimit.Error{Err: session.ErrThrottled, Wait: c.wait})

			headers, ok := errors.AsType[huma.HeadersError](err)
			require.True(t, ok)
			assert.Equal(t, []string{strconv.Itoa(c.want)}, headers.GetHeaders().Values(retryAfterHeader))
		})
	}
}

// An error without a wait gets no header.
func TestRetryAfterMissing(t *testing.T) {
	err := retryAfterProblem(requestProblem(http.StatusTooManyRequests), session.ErrThrottled)

	_, headers := errors.AsType[huma.HeadersError](err)
	assert.False(t, headers)
}

// The contract documents Retry-After on every 429.
func TestRetryAfterDocumented(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()

	var throttled int
	for path, item := range doc.Paths {
		for _, op := range []*huma.Operation{item.Get, item.Put, item.Post, item.Delete, item.Patch} {
			if op == nil {
				continue
			}
			response := op.Responses[strconv.Itoa(http.StatusTooManyRequests)]
			if response == nil {
				continue
			}
			throttled++
			header := response.Headers[retryAfterHeader]
			require.NotNil(t, header, path)
			assert.True(t, header.Required, path)
			assert.Equal(t, huma.TypeInteger, header.Schema.Type, path)
		}
	}
	assert.Equal(t, 21, throttled, "setup, sign-in, code sign-in, two-factor start, its code check, showing waiting codes, backup codes, turn-off, verification links, redeem, the probe, both registration writes, both password reset writes, both Gmail connection starts, the three sends, and an account's sync")
}
