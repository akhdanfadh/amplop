package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/account"
	"amplop/internal/emailaddress"
	"amplop/internal/session"
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
	device        session.Device
	calls         int
	listed        []session.Listed
	listErr       error
	listCalls     int
	endedID       uuid.UUID
	endErr        error
	endCalls      int
	endedOthers   int
	endOthersErr  error
	endOtherCalls int
	caller        session.Authenticated
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

func (f *fakeSessions) StartSignIn(_ context.Context, email, password string, device session.Device) (session.SignedIn, error) {
	f.calls++
	f.email, f.password, f.device = email, password, device
	return f.issued, f.err
}

func (f *fakeSessions) List(_ context.Context, current session.Authenticated) ([]session.Listed, error) {
	f.listCalls++
	f.caller = current
	return f.listed, f.listErr
}

func (f *fakeSessions) End(_ context.Context, current session.Authenticated, id uuid.UUID) error {
	f.endCalls++
	f.caller, f.endedID = current, id
	return f.endErr
}

func (f *fakeSessions) EndOthers(_ context.Context, current session.Authenticated) (int, error) {
	f.endOtherCalls++
	f.caller = current
	return f.endedOthers, f.endOthersErr
}

// referenceStorage supplies a session row for issuing test references through Current.
type referenceStorage struct{ session.Storage }

func (referenceStorage) ReadSessionIdentity(context.Context, [sha256.Size]byte) (session.IdentityState, error) {
	return session.IdentityState{User: account.User{ID: 42, Role: account.RoleMember}}, nil
}

func (referenceStorage) UpdateSession(_ context.Context, hash [sha256.Size]byte, update func(session.State) (session.Change, error)) error {
	now := time.Now()
	_, err := update(session.State{TokenHash: hash, ID: uuid.New(), UserID: 42, CreatedAt: now, LastUsedAt: now, ExpiresAt: now.Add(time.Hour)})
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

// requestAuthenticated sends a signed-in request with a same-origin write
// source, and body as JSON unless it is empty.
func requestAuthenticated(api *API, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "/api/v0"+path, strings.NewReader(body))
	request.Header.Set("Origin", "http://"+request.Host)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-secret"})
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	return response
}

// memberSessions returns a fake signed in without two-factor: a member with
// member access, or an administrator who still needs TOTP, with enrollment
// access.
func memberSessions(role account.Role) *fakeSessions {
	access := session.AccessMember
	if role == account.RoleAdmin {
		access = session.AccessEnrollment
	}
	return &fakeSessions{identity: session.Identity{
		User:   account.User{ID: 42, Email: "alice@example.com", Role: role},
		Access: access, TOTPRequired: role == account.RoleAdmin,
	}}
}

// assertSessionCookieCleared checks that response clears the session cookie
// when cleared is true, and sets no cookie otherwise.
func assertSessionCookieCleared(t *testing.T, response *httptest.ResponseRecorder, cleared bool) {
	t.Helper()
	cookies := response.Result().Cookies()
	if !cleared {
		assert.Empty(t, cookies)
		return
	}
	require.Len(t, cookies, 1)
	assert.Equal(t, sessionCookieName, cookies[0].Name)
	assert.Empty(t, cookies[0].Value)
	assert.Negative(t, cookies[0].MaxAge)
	// A __Host- cookie is cleared only by one with the same attributes.
	assert.Equal(t, "/", cookies[0].Path)
	assert.True(t, cookies[0].Secure)
	assert.True(t, cookies[0].HttpOnly)
}

// sendSessionRequest sends a same-origin request carrying a session cookie.
func sendSessionRequest(api *API, method, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "/api/v0"+path, nil)
	request.Header.Set("Origin", "http://"+request.Host)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "private-cookie-secret"})
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	return response
}

func postSession(api *API, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v0/sessions", strings.NewReader(body))
	request.Header.Set("Origin", "http://"+request.Host)
	request.Header.Set("Content-Type", jsonContentType)
	request.Header.Set("User-Agent", "amplop-test/1.0")
	request.AddCookie(&http.Cookie{Name: "__Host-amplop-session", Value: "old-browser-secret"})
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
	assert.Equal(t, session.Device{UserAgent: "amplop-test/1.0", Addr: netip.MustParseAddr("192.0.2.1")}, service.device)
	cookies := response.Result().Cookies()
	require.Len(t, cookies, 2)
	assert.Equal(t, challengeCookieName, cookies[1].Name)
	assert.Negative(t, cookies[1].MaxAge)
	cookie := cookies[0]
	assert.Equal(t, "__Host-amplop-session", cookie.Name)
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
		{"deactivated", account.ErrDeactivated, http.StatusForbidden, "/api/v0/problems/account-deactivated"},
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

// An email the service rejects is the same field error as one Huma rejects.
func TestCreateSessionRejectedEmail(t *testing.T) {
	service := &fakeSessions{err: emailaddress.ErrInvalid}

	response := postSession(New(discard, Services{Session: service}), signInHTTPBody)

	require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	body := decodeJSON(t, response.Body.Bytes())
	assert.Equal(t, problemPrefix+"validation-error", body["type"])
	assert.Equal(t, []any{map[string]any{"location": "body.email", "message": "Enter an email address, like name@example.com."}}, body["errors"])
	assert.Empty(t, response.Result().Cookies())
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
			assert.Equal(t, netip.MustParseAddr(c.want), service.device.Addr)
		})
	}
}

func TestSessionOpenAPI(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()
	path := doc.Paths["/sessions"]
	require.NotNil(t, path)
	require.NotNil(t, path.Post)
	assert.Equal(t, "createSession", path.Post.OperationID)
	for _, status := range []int{http.StatusCreated, http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests} {
		require.NotNil(t, path.Post.Responses[strconv.Itoa(status)])
	}
	assert.NotNil(t, path.Post.Responses["default"])
	assert.Len(t, path.Post.Responses, 5)
	assert.Equal(t, problemPrefix+"account-deactivated", path.Post.Responses["403"].Content[problemContentType].Example.(*problem).Type)
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
			assertSessionCookieCleared(t, response, true)
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
	assert.Equal(t, session.AccessEnrollment, op.Metadata[accessKey])
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

// ========================
// Session list
// ========================

func TestListSessions(t *testing.T) {
	signedIn := time.Date(2026, time.October, 30, 9, 0, 0, 0, time.UTC)
	for _, role := range []account.Role{account.RoleMember, account.RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			service := confirmedSessions(role)
			service.reference = checkedReference(t)
			service.listed = []session.Listed{
				{
					ID: uuid.MustParse("0b7f3a52-5d4e-4c1a-9f0e-2a6b8c9d1e2f"), Browser: "Firefox", OS: "Linux",
					SignedInFrom: netip.MustParseAddr("2001:db8::1"), CreatedAt: signedIn, LastUsedAt: signedIn.Add(time.Hour), Current: true,
				},
				{ID: uuid.MustParse("6c1d2e3f-4a5b-4c6d-8e7f-9a0b1c2d3e4f"), CreatedAt: signedIn.Add(-time.Hour), LastUsedAt: signedIn},
			}

			response := sendSessionRequest(New(discard, Services{Session: service}), http.MethodGet, "/sessions")

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.JSONEq(t, `{"data":{"sessions":[
				{"id":"0b7f3a52-5d4e-4c1a-9f0e-2a6b8c9d1e2f","browser":"Firefox","os":"Linux",
				 "signedInAt":"2026-10-30T09:00:00Z","lastActiveAt":"2026-10-30T10:00:00Z","signedInFrom":"2001:db8::1","current":true},
				{"id":"6c1d2e3f-4a5b-4c6d-8e7f-9a0b1c2d3e4f","browser":"","os":"",
				 "signedInAt":"2026-10-30T08:00:00Z","lastActiveAt":"2026-10-30T09:00:00Z","signedInFrom":"","current":false}
			]}}`, response.Body.String())
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			assert.Equal(t, service.reference, service.caller.Session)
		})
	}
}

func TestListSessionsEmpty(t *testing.T) {
	service := memberSessions(account.RoleMember)

	response := sendSessionRequest(New(discard, Services{Session: service}), http.MethodGet, "/sessions")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"sessions":[]}}`, response.Body.String(), "the web schema rejects null")
}

// ========================
// Session end
// ========================

func TestDeleteSession(t *testing.T) {
	service := memberSessions(account.RoleMember)
	service.reference = checkedReference(t)
	id := uuid.MustParse("0b7f3a52-5d4e-4c1a-9f0e-2a6b8c9d1e2f")

	response := sendSessionRequest(New(discard, Services{Session: service}), http.MethodDelete, "/sessions/"+id.String())

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"ended":1}}`, response.Body.String())
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	assert.Equal(t, id, service.endedID)
	assert.Equal(t, service.reference, service.caller.Session)
	assert.Zero(t, service.signOutCalls)
	assert.Empty(t, response.Result().Cookies(), "the current session's cookie stays")
}

func TestDeleteSessionErrors(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		typ    string
	}{
		{"not found", session.ErrNotFound, http.StatusNotFound, sessionNotFoundType},
		{"current", session.ErrCurrentSession, http.StatusConflict, sessionIsCurrentType},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			service := memberSessions(account.RoleMember)
			service.endErr = c.err

			response := sendSessionRequest(New(discard, Services{Session: service}), http.MethodDelete, "/sessions/0b7f3a52-5d4e-4c1a-9f0e-2a6b8c9d1e2f")

			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), `"type":"`+c.typ+`"`)
		})
	}
}

func TestDeleteSessionInvalidID(t *testing.T) {
	// totp is a real path segment, but only for POST.
	for _, id := range []string{"not-an-id", "totp", "0b7f3a52"} {
		t.Run(id, func(t *testing.T) {
			service := memberSessions(account.RoleMember)

			response := sendSessionRequest(New(discard, Services{Session: service}), http.MethodDelete, "/sessions/"+id)

			require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), `"location":"path.id"`)
			assert.NotContains(t, response.Body.String(), id, "the rejected value is not echoed")
			assert.Zero(t, service.endCalls)
		})
	}
}

func TestDeleteSessionRouting(t *testing.T) {
	service := memberSessions(account.RoleMember)
	api := New(discard, Services{Session: service})
	// The ID shares its first letter with the static current segment.
	id := uuid.MustParse("c0ffee00-5d4e-4c1a-9f0e-2a6b8c9d1e2f")

	signOut := sendSessionRequest(api, http.MethodDelete, "/sessions/current")
	end := sendSessionRequest(api, http.MethodDelete, "/sessions/"+id.String())

	require.Equal(t, http.StatusNoContent, signOut.Code, signOut.Body.String())
	require.Equal(t, http.StatusOK, end.Code, end.Body.String())
	assert.Equal(t, 1, service.signOutCalls)
	assert.Equal(t, 1, service.endCalls, "current is never read as a session ID")
	assert.Equal(t, id, service.endedID)
	assert.Zero(t, service.endOtherCalls)
}

// ========================
// Other sessions end
// ========================

func TestDeleteOtherSessions(t *testing.T) {
	service := confirmedSessions(account.RoleAdmin)
	service.reference = checkedReference(t)
	service.endedOthers = 2

	response := sendSessionRequest(New(discard, Services{Session: service}), http.MethodDelete, "/sessions/others")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"ended":2}}`, response.Body.String())
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	assert.Equal(t, 1, service.endOtherCalls)
	assert.Equal(t, service.reference, service.caller.Session)
	assert.Zero(t, service.endCalls, "others is never read as a session ID")
	assert.Zero(t, service.signOutCalls)
}

// ========================
// Session list and end, shared
// ========================

// sessionListAndEndRequests are one request per operation.
var sessionListAndEndRequests = []struct{ method, path string }{
	{http.MethodGet, "/sessions"},
	{http.MethodDelete, "/sessions/0b7f3a52-5d4e-4c1a-9f0e-2a6b8c9d1e2f"},
	{http.MethodDelete, "/sessions/others"},
}

func TestSessionListAndEndUnauthenticated(t *testing.T) {
	for _, r := range sessionListAndEndRequests {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			service := &fakeSessions{currentErr: session.ErrUnauthenticated}

			response := sendSessionRequest(New(discard, Services{Session: service}), r.method, r.path)

			require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
			assert.Zero(t, service.listCalls+service.endCalls+service.endOtherCalls)
		})
	}
}

func TestSessionEndSourceRequired(t *testing.T) {
	for _, path := range []string{"/sessions/0b7f3a52-5d4e-4c1a-9f0e-2a6b8c9d1e2f", "/sessions/others"} {
		for _, site := range []string{"cross-site", ""} {
			t.Run(path+" "+site, func(t *testing.T) {
				service := memberSessions(account.RoleMember)
				request := httptest.NewRequest(http.MethodDelete, "/api/v0"+path, nil)
				request.Header.Set("Sec-Fetch-Site", site)
				request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "private-cookie-secret"})
				response := httptest.NewRecorder()

				New(discard, Services{Session: service}).Handler().ServeHTTP(response, request)

				require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
				assert.Zero(t, service.currentCalls)
				assert.Zero(t, service.endCalls+service.endOtherCalls)
			})
		}
	}
}

func TestSessionListAndEndErrors(t *testing.T) {
	for _, r := range sessionListAndEndRequests {
		for _, c := range []struct {
			name   string
			err    error
			status int
			typ    string
		}{
			// The session ended or expired after the access check.
			{"stale", session.ErrUnauthenticated, http.StatusUnauthorized, problemPrefix + "unauthenticated"},
			{"unexpected", errors.New("connection lost"), http.StatusInternalServerError, problemPrefix + "internal-error"},
		} {
			t.Run(r.method+" "+r.path+" "+c.name, func(t *testing.T) {
				service := memberSessions(account.RoleMember)
				service.listErr, service.endErr, service.endOthersErr = c.err, c.err, c.err

				response := sendSessionRequest(New(discard, Services{Session: service}), r.method, r.path)

				require.Equal(t, c.status, response.Code, response.Body.String())
				assert.Contains(t, response.Body.String(), `"type":"`+c.typ+`"`)
				assert.NotContains(t, response.Body.String(), "connection lost")
				assert.Equal(t, 1, service.listCalls+service.endCalls+service.endOtherCalls)
				assertSessionCookieCleared(t, response, c.status == http.StatusUnauthorized)
			})
		}
	}
}

func TestSessionListAndEndOpenAPI(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()
	for _, c := range []struct {
		op       *huma.Operation
		id       string
		statuses []int
	}{
		{doc.Paths["/sessions"].Get, "listSessions", []int{http.StatusOK, http.StatusUnauthorized, http.StatusForbidden}},
		{doc.Paths["/sessions/{id}"].Delete, "deleteSession", []int{http.StatusOK, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict}},
		{doc.Paths["/sessions/others"].Delete, "deleteOtherSessions", []int{http.StatusOK, http.StatusUnauthorized, http.StatusForbidden}},
	} {
		require.NotNil(t, c.op, c.id)
		assert.Equal(t, c.id, c.op.OperationID)
		assert.Equal(t, session.AccessMember, c.op.Metadata[accessKey], c.id)
		assert.Equal(t, []map[string][]string{{sessionSecuritySchemeName: {}}}, c.op.Security, c.id)
		want := []string{"default"}
		for _, status := range c.statuses {
			want = append(want, strconv.Itoa(status))
		}
		assert.ElementsMatch(t, want, slices.Collect(maps.Keys(c.op.Responses)), c.id)
	}
	listed := doc.Components.Schemas.Map()["ListedSession"]
	require.NotNil(t, listed)
	assert.ElementsMatch(t, []string{"id", "browser", "os", "signedInAt", "lastActiveAt", "signedInFrom", "current"}, listed.Required)
}
