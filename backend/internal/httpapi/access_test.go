package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/session"
)

func TestAccessGate(t *testing.T) {
	member := session.Identity{User: account.User{ID: 42, Role: account.RoleMember}, Access: session.AccessMember}
	adminPending := session.Identity{User: account.User{ID: 42, Role: account.RoleAdmin}, Access: session.AccessEnrollment, TOTPRequired: true}
	admin := session.Identity{User: account.User{ID: 42, Role: account.RoleAdmin}, Access: session.AccessAdmin}
	for _, c := range []struct {
		name     string
		level    any
		identity session.Identity
		cookie   string
		err      error
		code     int
		typ      string
	}{
		{"public", session.AccessPublic, member, "", nil, http.StatusOK, ""},
		{"no cookie", session.AccessMember, member, "", nil, http.StatusUnauthorized, "unauthenticated"},
		{"expired", session.AccessMember, member, "expired-secret", session.ErrUnauthenticated, http.StatusUnauthorized, "unauthenticated"},
		{"member", session.AccessMember, member, "test-secret", nil, http.StatusOK, ""},
		{"pending admin", session.AccessEnrollment, adminPending, "test-secret", nil, http.StatusOK, ""},
		{"member at enrollment level", session.AccessEnrollment, member, "test-secret", nil, http.StatusOK, ""},
		{"pending admin as member", session.AccessMember, adminPending, "test-secret", nil, http.StatusForbidden, "totp-required"},
		{"member as admin", session.AccessAdmin, member, "test-secret", nil, http.StatusForbidden, "forbidden"},
		{"pending admin as admin", session.AccessAdmin, adminPending, "test-secret", nil, http.StatusForbidden, "totp-required"},
		{"member cannot claim enrollment", session.AccessMember, session.Identity{User: member.User, Access: session.AccessEnrollment}, "test-secret", nil, http.StatusForbidden, "forbidden"},
		{"admin", session.AccessAdmin, admin, "test-secret", nil, http.StatusOK, ""},
		{"omitted defaults admin", nil, adminPending, "test-secret", nil, http.StatusForbidden, "totp-required"},
		{"wrong metadata type", "public", adminPending, "test-secret", nil, http.StatusForbidden, "totp-required"},
		{"unknown level", session.Access(-1), adminPending, "test-secret", nil, http.StatusForbidden, "totp-required"},
		{"unknown high level", session.Access(99), member, "test-secret", nil, http.StatusForbidden, "forbidden"},
		{"member cannot gain admin", session.AccessAdmin, session.Identity{User: member.User, Access: session.AccessAdmin}, "test-secret", nil, http.StatusForbidden, "forbidden"},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeSessions{identity: c.identity, currentErr: c.err}
			api := New(discard, Services{Session: service})
			called := false
			huma.Register(api.api, huma.Operation{OperationID: "testAccess", Method: http.MethodGet, Path: "/test-access", Metadata: map[string]any{accessKey: c.level}}, func(ctx context.Context, _ *struct{}) (*output[healthStatus], error) {
				called = true
				if c.level != session.AccessPublic {
					assert.Equal(t, c.identity, ctx.Value(sessionKey{}).(session.Authenticated).Identity)
				}
				return respondData(healthStatus{Status: "ok"}), nil
			})
			request := httptest.NewRequest(http.MethodGet, "/api/v0/test-access", nil)
			if c.cookie != "" {
				request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: c.cookie})
			}
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			require.Equal(t, c.code, response.Code, response.Body.String())
			assert.Equal(t, c.code == http.StatusOK, called)
			if c.typ != "" {
				assert.Equal(t, problemPrefix+c.typ, decodeJSON(t, response.Body.Bytes())["type"])
				assert.Equal(t, problemContentType, response.Header().Get("Content-Type"))
			}
			if c.code == http.StatusUnauthorized {
				cookies := response.Result().Cookies()
				require.Len(t, cookies, 1)
				assert.Equal(t, sessionCookieName, cookies[0].Name)
				assert.Negative(t, cookies[0].MaxAge)
				assert.Equal(t, "/", cookies[0].Path)
				assert.True(t, cookies[0].Secure)
				assert.True(t, cookies[0].HttpOnly)
			} else {
				assert.Empty(t, response.Result().Cookies())
			}
			if c.level == session.AccessPublic || c.cookie == "" {
				assert.Zero(t, service.currentCalls)
			} else {
				assert.Equal(t, 1, service.currentCalls)
			}
		})
	}
}

func TestCurrentSessionHTTP(t *testing.T) {
	service := &fakeSessions{}
	api := New(discard, Services{Session: service})
	for i, role := range []account.Role{account.RoleMember, account.RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			service.identity = session.Identity{User: account.User{ID: 42, Email: "admin@example.com", Role: role}, Access: session.AccessMember, TOTPRequired: role == account.RoleAdmin, EmailVerified: role == account.RoleMember}
			request := httptest.NewRequest(http.MethodGet, "/api/v0/sessions/current", nil)
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "private-cookie-secret"})
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			data := decodeJSON(t, response.Body.Bytes())["data"]
			assert.Equal(t, map[string]any{"email": "admin@example.com", "role": string(role), "totpRequired": role == account.RoleAdmin, "emailVerified": role == account.RoleMember}, data)
			assert.Equal(t, "private-cookie-secret", service.currentSecret)
			assert.Equal(t, i+1, service.currentCalls)
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			assert.Empty(t, response.Result().Cookies())
			assert.NotContains(t, response.Body.String(), "private-cookie-secret")
		})
	}
}

func TestAccessBeforeValidation(t *testing.T) {
	api := New(discard, Services{})
	called := false
	huma.Register(api.api, huma.Operation{OperationID: "testProtectedWrite", Method: http.MethodPost, Path: "/protected-write"}, func(_ context.Context, _ *struct {
		Body struct {
			Required string `json:"required"`
		}
	}) (*output[healthStatus], error) {
		called = true
		return respondData(healthStatus{Status: "ok"}), nil
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v0/protected-write", bytes.NewBufferString("{"))
	request.Header.Set("Origin", "http://"+request.Host)
	request.Header.Set("Content-Type", jsonContentType)
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	require.Equal(t, http.StatusUnauthorized, response.Code)
	assert.Equal(t, problemPrefix+"unauthenticated", decodeJSON(t, response.Body.Bytes())["type"])
	assert.False(t, called)
}

func TestAccessStorageFailure(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	service := &fakeSessions{currentErr: errors.New("session storage unavailable")}
	api := New(logger, Services{Session: service})
	request := httptest.NewRequest(http.MethodGet, "/api/v0/sessions/current", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "private-cookie-secret"})
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	require.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Equal(t, problemPrefix+"internal-error", decodeJSON(t, response.Body.Bytes())["type"])
	assert.Empty(t, response.Result().Cookies(), "keep the cookie so the request can be retried")
	assert.Contains(t, logs.String(), "session storage unavailable")
	assert.NotContains(t, logs.String(), "private-cookie-secret")
	assert.NotContains(t, response.Body.String(), "session storage unavailable")
}

func TestAccessDeclarations(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()
	for _, op := range []*huma.Operation{
		doc.Paths["/health"].Get,
		doc.Paths["/probe/events"].Post,
		doc.Paths["/setup"].Get, doc.Paths["/setup"].Post,
		doc.Paths["/sessions"].Post,
	} {
		require.NotNil(t, op)
		assert.Equal(t, session.AccessPublic, op.Metadata[accessKey], op.OperationID)
	}
	current := doc.Paths["/sessions/current"].Get
	require.NotNil(t, current)
	assert.Equal(t, session.AccessEnrollment, current.Metadata[accessKey])
	assert.Equal(t, "getCurrentSession", current.OperationID)
	assert.Equal(t, []map[string][]string{{sessionSecuritySchemeName: {}}}, current.Security)
	require.Len(t, current.Responses, 3)
	assert.NotNil(t, current.Responses[strconv.Itoa(http.StatusOK)])
	assert.NotNil(t, current.Responses[strconv.Itoa(http.StatusUnauthorized)])
	assert.NotNil(t, current.Responses["default"])
	scheme := doc.Components.SecuritySchemes[sessionSecuritySchemeName]
	require.NotNil(t, scheme)
	assert.Equal(t, "cookie", scheme.In)
	assert.Equal(t, "apiKey", scheme.Type)
	assert.Equal(t, sessionCookieName, scheme.Name)
	result := doc.Components.Schemas.Map()["CurrentSession"]
	require.NotNil(t, result)
	assert.ElementsMatch(t, []string{"email", "role", "totpRequired", "emailVerified"}, result.Required)
	require.NotNil(t, result.Properties["role"])
	assert.ElementsMatch(t, []any{"member", "admin"}, result.Properties["role"].Enum)
}
