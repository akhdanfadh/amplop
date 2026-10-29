package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
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

// ========================
// Test helpers
// ========================

const (
	signInHTTPBody      = `{"email":"Admin@Example.COM","password":"a long sign-in password"}`
	encodedSecretLength = 43
)

type fakeSessions struct {
	signOutErr    error
	signOutCalls  int
	signedOut     session.Reference
	reference     session.Reference
	identity      session.Identity
	currentErr    error
	currentCalls  int
	currentSecret string
	issued        session.SignedIn
	err           error
	email         string
	password      string
	addr          netip.Addr
	calls         int
}

func (f *fakeSessions) SignOut(_ context.Context, ref session.Reference) error {
	f.signOutCalls++
	f.signedOut = ref
	return f.signOutErr
}

func (f *fakeSessions) Current(_ context.Context, secret string) (session.Authenticated, error) {
	f.currentCalls++
	f.currentSecret = secret
	return session.Authenticated{Identity: f.identity, Session: f.reference}, f.currentErr
}

func (f *fakeSessions) StartSignIn(_ context.Context, email, password string, addr netip.Addr) (session.SignedIn, error) {
	f.calls++
	f.email, f.password, f.addr = email, password, addr
	return f.issued, f.err
}

// referenceStorage supplies a session row for issuing test references through Current.
type referenceStorage struct{ session.Storage }

func (referenceStorage) ReadSessionIdentity(context.Context, [sha256.Size]byte) (session.IdentityState, error) {
	return session.IdentityState{User: account.User{ID: 42, Role: account.RoleMember}}, nil
}

func (referenceStorage) UpdateSession(_ context.Context, hash [sha256.Size]byte, update func(session.State) (session.Change, error)) error {
	now := time.Now()
	_, err := update(session.State{TokenHash: hash, UserID: 42, CreatedAt: now, LastUsedAt: now, ExpiresAt: now.Add(time.Hour)})
	return err
}

func checkedReference(t *testing.T) session.Reference {
	t.Helper()
	service := session.NewService(referenceStorage{}, nil, nil, time.Now)
	current, err := service.Current(t.Context(), strings.Repeat("A", encodedSecretLength))
	require.NoError(t, err)
	require.NotZero(t, current.Session)
	return current.Session
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

// ========================
// Sign-in
// ========================

func TestCreateSession(t *testing.T) {
	service := &fakeSessions{issued: session.SignedIn{
		Step:   session.SignInStepComplete,
		Secret: strings.Repeat("A", encodedSecretLength), ExpiresAt: time.Now().Add(2 * time.Hour),
	}}

	response := postSession(New(discard, Services{Session: service}), signInHTTPBody)

	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"step":"complete"}}`, response.Body.String())
	assert.Equal(t, "Admin@Example.COM", service.email)
	assert.Equal(t, "a long sign-in password", service.password)
	assert.Equal(t, netip.MustParseAddr("192.0.2.1"), service.addr)
	cookies := response.Result().Cookies()
	require.Len(t, cookies, 2)
	assert.Equal(t, challengeCookieName, cookies[1].Name)
	assert.Negative(t, cookies[1].MaxAge)
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
	assert.Equal(t, []any{"complete", "totp"}, result.Properties["step"].Enum)
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

// ========================
// Sign-out
// ========================

func TestDeleteCurrentSession(t *testing.T) {
	ref := checkedReference(t)
	for _, role := range []account.Role{account.RoleMember, account.RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			service := &fakeSessions{identity: session.Identity{
				User: account.User{ID: 42, Role: role}, Access: session.AccessMember, TOTPRequired: role == account.RoleAdmin,
			}, reference: ref}
			api := New(discard, Services{Session: service})
			request := httptest.NewRequest(http.MethodDelete, "/api/v0/sessions/current", nil)
			request.Header.Set("Origin", "http://"+request.Host)
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "private-cookie-secret"})
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
			assert.Empty(t, response.Body.String())
			assert.Equal(t, 1, service.signOutCalls)
			assert.Equal(t, ref, service.signedOut)
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			cookies := response.Result().Cookies()
			require.Len(t, cookies, 1)
			assert.Equal(t, sessionCookieName, cookies[0].Name)
			assert.Empty(t, cookies[0].Value)
			assert.Negative(t, cookies[0].MaxAge)
			assert.Equal(t, "/", cookies[0].Path)
			assert.Empty(t, cookies[0].Domain)
			assert.True(t, cookies[0].Secure)
			assert.True(t, cookies[0].HttpOnly)
			assert.Equal(t, http.SameSiteLaxMode, cookies[0].SameSite)
		})
	}
}

func TestSignOutUnauthenticated(t *testing.T) {
	for _, secret := range []string{"", "expired-secret", "missing-secret", "invalid-secret"} {
		t.Run(secret, func(t *testing.T) {
			service := &fakeSessions{currentErr: session.ErrUnauthenticated}
			api := New(discard, Services{Session: service})
			request := httptest.NewRequest(http.MethodDelete, "/api/v0/sessions/current", nil)
			request.Header.Set("Origin", "http://"+request.Host)
			if secret != "" {
				request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: secret})
			}
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
			assert.Equal(t, problemPrefix+"unauthenticated", decodeJSON(t, response.Body.Bytes())["type"])
			assert.Zero(t, service.signOutCalls)
			cookies := response.Result().Cookies()
			require.Len(t, cookies, 1)
			assert.Equal(t, sessionCookieName, cookies[0].Name)
			assert.Empty(t, cookies[0].Value)
			assert.Negative(t, cookies[0].MaxAge)
		})
	}
}

func TestSignOutDuplicateCookies(t *testing.T) {
	ref := checkedReference(t)
	service := &fakeSessions{identity: session.Identity{
		User: account.User{ID: 42, Role: account.RoleMember}, Access: session.AccessMember,
	}, reference: ref}
	api := New(discard, Services{Session: service})
	request := httptest.NewRequest(http.MethodDelete, "/api/v0/sessions/current", nil)
	request.Header.Set("Origin", "http://"+request.Host)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "checked-session"})
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "different-session"})
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
	assert.Equal(t, "checked-session", service.currentSecret)
	assert.Equal(t, 1, service.currentCalls)
	assert.Equal(t, 1, service.signOutCalls)
	assert.Equal(t, ref, service.signedOut)
}

func TestSignOutStorageFailure(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	lost := errors.New("session storage unavailable")
	service := &fakeSessions{identity: session.Identity{
		User: account.User{ID: 42, Role: account.RoleMember}, Access: session.AccessMember,
	}, signOutErr: lost}
	api := New(logger, Services{Session: service})
	request := httptest.NewRequest(http.MethodDelete, "/api/v0/sessions/current", nil)
	request.Header.Set("Origin", "http://"+request.Host)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "private-cookie-secret"})
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
	assert.Equal(t, problemPrefix+"internal-error", decodeJSON(t, response.Body.Bytes())["type"])
	assert.Empty(t, response.Result().Cookies(), "keep the cookie so deletion can be retried")
	assert.NotContains(t, response.Body.String(), lost.Error())
	assert.NotContains(t, response.Body.String(), "private-cookie-secret")
	assert.NotContains(t, logs.String(), "private-cookie-secret")
}

func TestSignOutSourceRequired(t *testing.T) {
	for _, site := range []string{"cross-site", ""} {
		service := &fakeSessions{}
		api := New(discard, Services{Session: service})
		request := httptest.NewRequest(http.MethodDelete, "/api/v0/sessions/current", nil)
		request.Header.Set("Sec-Fetch-Site", site)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "private-cookie-secret"})
		response := httptest.NewRecorder()

		api.Handler().ServeHTTP(response, request)

		require.Equal(t, http.StatusForbidden, response.Code)
		assert.Zero(t, service.currentCalls)
		assert.Zero(t, service.signOutCalls)
		assert.Empty(t, response.Result().Cookies())
	}
}

func TestSignOutOpenAPI(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()
	path := doc.Paths["/sessions/current"]
	require.NotNil(t, path)
	require.NotNil(t, path.Delete)
	op := path.Delete
	assert.Equal(t, "deleteCurrentSession", op.OperationID)
	assert.Equal(t, session.AccessMember, op.Metadata[accessKey])
	assert.Equal(t, []map[string][]string{{sessionSecuritySchemeName: {}}}, op.Security)
	assert.Empty(t, op.Parameters, "the cookie is documented by the security scheme")
	require.Len(t, op.Responses, 3)
	noContent := op.Responses[strconv.Itoa(http.StatusNoContent)]
	require.NotNil(t, noContent)
	assert.Empty(t, noContent.Content)
	assert.NotNil(t, noContent.Headers["Set-Cookie"])
	assert.NotNil(t, op.Responses[strconv.Itoa(http.StatusUnauthorized)])
	assert.NotNil(t, op.Responses["default"])
}
