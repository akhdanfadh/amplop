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
	"inbok/internal/registration"
	"inbok/internal/session"
)

type fakeRegistration struct {
	settings registration.Settings
	err      error

	calls   int
	actorID int64
	state   registration.State
}

func (f *fakeRegistration) Settings(context.Context) (registration.Settings, error) {
	f.calls++
	return f.settings, f.err
}

func (f *fakeRegistration) Save(_ context.Context, actorID int64, state registration.State) (registration.Settings, error) {
	f.calls++
	f.actorID, f.state = actorID, state
	return f.settings, f.err
}

func TestGetRegistration(t *testing.T) {
	for _, c := range []struct {
		status registration.Status
		want   string
	}{
		{registration.StatusOpen, `{"data":{"open":true}}`},
		{registration.StatusClosed, `{"data":{"open":false}}`},
		// Visitors cannot register while paused, and need not know why.
		{registration.StatusPaused, `{"data":{"open":false}}`},
	} {
		t.Run(string(c.status), func(t *testing.T) {
			service := &fakeRegistration{settings: registration.Settings{Status: c.status}}
			api := New(discard, Services{Registration: service})
			request := httptest.NewRequest(http.MethodGet, "/api/v0/registration", nil)
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.JSONEq(t, c.want, response.Body.String())
		})
	}
}

func TestGetRegistrationSettings(t *testing.T) {
	for _, c := range []struct {
		name     string
		settings registration.Settings
		want     string
	}{
		{"closed", registration.Settings{Status: registration.StatusClosed}, `{"data":{"status":"closed","systemEmailTested":false}}`},
		{"open", registration.Settings{Status: registration.StatusOpen, SystemEmailTested: true}, `{"data":{"status":"open","systemEmailTested":true}}`},
		{"paused", registration.Settings{Status: registration.StatusPaused}, `{"data":{"status":"paused","systemEmailTested":false}}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			api := New(discard, Services{Registration: &fakeRegistration{settings: c.settings}, Session: adminSessions()})

			response := requestAuthenticated(api, http.MethodGet, "/registration/settings", "")

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.JSONEq(t, c.want, response.Body.String())
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		})
	}
}

func TestSaveRegistrationSettings(t *testing.T) {
	for _, c := range []struct {
		body string
		want registration.State
	}{
		{`{"open":true}`, registration.StateOpen},
		{`{"open":false}`, registration.StateClosed},
	} {
		t.Run(c.body, func(t *testing.T) {
			service := &fakeRegistration{settings: registration.Settings{Status: registration.StatusOpen, SystemEmailTested: true}}
			api := New(discard, Services{Registration: service, Session: adminSessions()})

			response := requestAuthenticated(api, http.MethodPut, "/registration/settings", c.body)

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.Equal(t, int64(42), service.actorID, "the actor is the signed-in administrator")
			assert.Equal(t, c.want, service.state)
			assert.JSONEq(t, `{"data":{"status":"open","systemEmailTested":true}}`, response.Body.String())
		})
	}
}

func TestSaveRegistrationSettingsInvalid(t *testing.T) {
	for _, body := range []string{`{}`, `{"open":"yes"}`} {
		t.Run(body, func(t *testing.T) {
			service := &fakeRegistration{}
			api := New(discard, Services{Registration: service, Session: adminSessions()})

			response := requestAuthenticated(api, http.MethodPut, "/registration/settings", body)

			require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			assert.Zero(t, service.calls)
		})
	}
}

func TestSaveRegistrationNotTested(t *testing.T) {
	api := New(discard, Services{Registration: &fakeRegistration{err: registration.ErrSystemEmailNotTested}, Session: adminSessions()})

	response := requestAuthenticated(api, http.MethodPut, "/registration/settings", `{"open":true}`)

	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	problem := decodeJSON(t, response.Body.Bytes())
	assert.Equal(t, problemPrefix+"system-email-not-tested", problem["type"])
	assert.Equal(t, "Save the system email settings and send a test message that passes before opening registration.", problem["detail"])
}

// Only administrators with confirmed TOTP read or change the setting; anyone
// may ask whether registration is open.
func TestRegistrationAccess(t *testing.T) {
	member := &fakeSessions{identity: session.Identity{User: account.User{ID: 42, Role: account.RoleMember}, Access: session.AccessMember}}
	for _, c := range []struct {
		method, path, body string
	}{
		{http.MethodGet, "/registration/settings", ""},
		{http.MethodPut, "/registration/settings", `{"open":true}`},
	} {
		t.Run(c.method, func(t *testing.T) {
			service := &fakeRegistration{}
			api := New(discard, Services{Registration: service, Session: member})

			response := requestAuthenticated(api, c.method, c.path, c.body)

			require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
			assert.Zero(t, service.calls)
		})
	}

	public := New(discard, Services{}).api.OpenAPI().Paths["/registration"].Get
	require.NotNil(t, public)
	assert.Equal(t, session.AccessPublic, public.Metadata[accessKey])
}

// Unexpected failures are a fixed 500 with the cause only in the log.
func TestRegistrationUnexpectedFailure(t *testing.T) {
	failure := errors.New("private storage cause")
	for _, c := range []struct {
		name, method, path, body string
	}{
		{"public", http.MethodGet, "/registration", ""},
		{"get", http.MethodGet, "/registration/settings", ""},
		{"save", http.MethodPut, "/registration/settings", `{"open":false}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			var logs bytes.Buffer
			api := New(slog.New(slog.NewTextHandler(&logs, nil)), Services{Registration: &fakeRegistration{err: failure}, Session: adminSessions()})

			response := requestAuthenticated(api, c.method, c.path, c.body)

			require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
			assert.NotContains(t, response.Body.String(), "private storage cause")
			assert.Contains(t, logs.String(), "private storage cause")
		})
	}
}
