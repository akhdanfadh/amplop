package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/session"
)

const signInHTTPBody = `{"email":"Admin@Example.COM","password":"a long sign-in password"}`

type fakeSessions struct {
	identity      session.Identity
	currentErr    error
	currentCalls  int
	currentSecret string
	issued        session.Issued
	err           error
	email         string
	password      string
	addr          netip.Addr
	calls         int
}

func (f *fakeSessions) Current(_ context.Context, secret string) (session.Identity, error) {
	f.currentCalls++
	f.currentSecret = secret
	return f.identity, f.currentErr
}

func (f *fakeSessions) SignIn(_ context.Context, email, password string, addr netip.Addr) (session.Issued, error) {
	f.calls++
	f.email, f.password, f.addr = email, password, addr
	return f.issued, f.err
}

func postSession(api *API, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v0/sessions", strings.NewReader(body))
	request.Header.Set("Origin", "http://"+request.Host)
	request.Header.Set("Content-Type", jsonContentType)
	request.AddCookie(&http.Cookie{Name: "__Host-inbok-session", Value: "old-browser-secret"})
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	return response
}

func TestCreateSession(t *testing.T) {
	service := &fakeSessions{issued: session.Issued{
		Secret: strings.Repeat("A", 43), State: session.State{ExpiresAt: time.Now().Add(2 * time.Hour)},
	}}

	response := postSession(New(discard, Services{Session: service}), signInHTTPBody)

	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"step":"complete"}}`, response.Body.String())
	assert.Equal(t, "Admin@Example.COM", service.email)
	assert.Equal(t, "a long sign-in password", service.password)
	assert.Equal(t, netip.MustParseAddr("192.0.2.1"), service.addr)
	cookies := response.Result().Cookies()
	require.Len(t, cookies, 1)
	cookie := cookies[0]
	assert.Equal(t, "__Host-inbok-session", cookie.Name)
	assert.Equal(t, service.issued.Secret, cookie.Value)
	assert.True(t, cookie.Secure)
	assert.True(t, cookie.HttpOnly)
	assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
	assert.Equal(t, "/", cookie.Path)
	assert.Empty(t, cookie.Domain)
	assert.InDelta(t, int((2*time.Hour)/time.Second), cookie.MaxAge, 2)
	assert.NotContains(t, response.Body.String(), service.issued.Secret)
}

func TestCreateSessionErrors(t *testing.T) {
	lost := errors.New("database connection lost")
	for _, c := range []struct {
		name string
		err  error
		code int
		typ  string
	}{
		{"credentials", account.ErrInvalidCredentials, http.StatusUnauthorized, "/api/v0/problems/invalid-credentials"},
		{"throttled", session.ErrThrottled, http.StatusTooManyRequests, "/api/v0/problems/sign-in-throttled"},
		{"unexpected", lost, http.StatusInternalServerError, "/api/v0/problems/internal-error"},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeSessions{err: c.err}
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))

			response := postSession(New(logger, Services{Session: service}), signInHTTPBody)

			require.Equal(t, c.code, response.Code, response.Body.String())
			assert.Equal(t, c.typ, decodeJSON(t, response.Body.Bytes())["type"])
			assert.Equal(t, problemContentType, response.Header().Get("Content-Type"))
			assert.Empty(t, response.Result().Cookies())
			assert.NotContains(t, response.Body.String(), lost.Error())
			for _, secret := range []string{"Admin@Example.COM", "a long sign-in password", "old-browser-secret"} {
				assert.NotContains(t, response.Body.String(), secret)
				assert.NotContains(t, logs.String(), secret)
			}
		})
	}
}

func TestSignInValidation(t *testing.T) {
	for _, c := range []struct {
		name, body string
		code       int
		calls      int
	}{
		{"short password still checked", `{"email":"a@example.com","password":"wrong"}`, http.StatusUnauthorized, 1},
		{"empty password still checked", `{"email":"a@example.com","password":""}`, http.StatusUnauthorized, 1},
		{"Unicode password", `{"email":"a@example.com","password":"` + strings.Repeat("\U0001f512", 128) + `"}`, http.StatusUnauthorized, 1},
		{"long password", `{"email":"a@example.com","password":"` + strings.Repeat("p", 129) + `"}`, http.StatusUnprocessableEntity, 0},
		{"invalid email", `{"email":"private-invalid-email","password":"wrong"}`, http.StatusUnprocessableEntity, 0},
		{"missing password", `{"email":"a@example.com"}`, http.StatusUnprocessableEntity, 0},
		{"malformed", `{"email":`, http.StatusBadRequest, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeSessions{err: account.ErrInvalidCredentials}

			response := postSession(New(discard, Services{Session: service}), c.body)

			require.Equal(t, c.code, response.Code, response.Body.String())
			assert.Equal(t, c.calls, service.calls)
			assert.Empty(t, response.Result().Cookies())
			assert.NotContains(t, response.Body.String(), "private-invalid-email")
		})
	}
}

func TestSignInClientAddress(t *testing.T) {
	for _, c := range []struct {
		name    string
		proxies []string
		remote  string
		want    string
	}{
		{"direct", nil, "192.0.2.10:1234", "192.0.2.10"},
		{"trusted proxy", []string{"10.0.0.0/8"}, "10.1.2.3:1234", "198.51.100.20"},
		{"untrusted proxy", []string{"10.0.0.0/8"}, "192.0.2.10:1234", "192.0.2.10"},
		{"IPv6", nil, "[2001:db8::1]:1234", "2001:db8::1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeSessions{err: account.ErrInvalidCredentials}
			api := New(discard, Services{Session: service})
			require.NoError(t, api.SetTrustedProxies(c.proxies))
			request := httptest.NewRequest(http.MethodPost, "/api/v0/sessions", strings.NewReader(signInHTTPBody))
			request.Header.Set("Origin", "http://"+request.Host)
			request.Header.Set("Content-Type", jsonContentType)
			request.Header.Set("X-Forwarded-For", "198.51.100.20")
			request.RemoteAddr = c.remote
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			require.Equal(t, http.StatusUnauthorized, response.Code)
			assert.Equal(t, netip.MustParseAddr(c.want), service.addr)
		})
	}
}

func TestSessionOpenAPI(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()
	path := doc.Paths["/sessions"]
	require.NotNil(t, path)
	require.NotNil(t, path.Post)
	assert.Equal(t, "createSession", path.Post.OperationID)
	for _, status := range []int{http.StatusCreated, http.StatusUnauthorized, http.StatusTooManyRequests} {
		require.NotNil(t, path.Post.Responses[strconv.Itoa(status)])
	}
	assert.NotNil(t, path.Post.Responses["default"])
	assert.Len(t, path.Post.Responses, 4)
	result := doc.Components.Schemas.Map()["SignInResult"]
	require.NotNil(t, result)
	require.NotNil(t, result.Properties["step"])
	assert.Equal(t, []any{"complete"}, result.Properties["step"].Enum)
	input := doc.Components.Schemas.Map()["NewSession"]
	require.NotNil(t, input)
	assert.True(t, input.Properties["password"].WriteOnly)
	created := path.Post.Responses[strconv.Itoa(http.StatusCreated)]
	require.NotNil(t, created.Headers["Set-Cookie"])
}

func TestSignInSourceRequired(t *testing.T) {
	for _, site := range []string{"cross-site", ""} {
		service := &fakeSessions{}
		api := New(discard, Services{Session: service})
		request := httptest.NewRequest(http.MethodPost, "/api/v0/sessions", strings.NewReader(signInHTTPBody))
		request.Header.Set("Content-Type", jsonContentType)
		request.Header.Set("Sec-Fetch-Site", site)
		response := httptest.NewRecorder()

		api.Handler().ServeHTTP(response, request)

		require.Equal(t, http.StatusForbidden, response.Code)
		assert.Zero(t, service.calls)
		assert.Empty(t, response.Result().Cookies())
	}
}

func TestInvalidClientAddress(t *testing.T) {
	for _, path := range []string{"/sessions", "/setup"} {
		accounts := &fakeAccounts{}
		sessions := &fakeSessions{}
		limiter := &fakeThrottle{}
		api := New(discard, Services{Account: accounts, Session: sessions, Throttle: limiter})
		body := signInHTTPBody
		if path == "/setup" {
			body = setupBody(t, setupFields())
		}
		request := httptest.NewRequest(http.MethodPost, "/api/v0"+path, strings.NewReader(body))
		request.Header.Set("Content-Type", jsonContentType)
		request.Header.Set("Origin", "http://"+request.Host)
		request.RemoteAddr = "invalid"
		response := httptest.NewRecorder()

		api.Handler().ServeHTTP(response, request)

		require.Equal(t, http.StatusInternalServerError, response.Code)
		assert.Equal(t, "/api/v0/problems/internal-error", decodeJSON(t, response.Body.Bytes())["type"])
		assert.Zero(t, sessions.calls)
		assert.Zero(t, limiter.begun)
		assert.Empty(t, accounts.created)
		assert.Empty(t, response.Result().Cookies())
	}
}
