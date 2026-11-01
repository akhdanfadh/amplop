package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/session"
	"inbok/internal/systememail"
)

type fakeSystemEmail struct {
	settings *systememail.Settings
	saved    systememail.Settings
	err      error

	calls   int
	actorID int64
	input   systememail.Input
}

func (f *fakeSystemEmail) Settings(context.Context) (*systememail.Settings, error) {
	f.calls++
	return f.settings, f.err
}

func (f *fakeSystemEmail) Save(_ context.Context, actorID int64, input systememail.Input) (systememail.Settings, error) {
	f.calls++
	f.actorID, f.input = actorID, input
	return f.saved, f.err
}

type fakeTestMessages struct {
	result    systememail.TestMessageResult
	err       error
	calls     int
	recipient string
}

func (f *fakeTestMessages) Send(_ context.Context, recipient string) (systememail.TestMessageResult, error) {
	f.calls++
	f.recipient = recipient
	return f.result, f.err
}

const systemEmailBody = `{"host":"smtp.example.com","port":587,"security":"starttls","username":"inbok@example.com",` +
	`"password":"app password 7f3c","fromAddress":"inbok@example.com","fromName":"inbok"}`

var systemEmailTested = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

func systemEmailSettingsFixture() systememail.Settings {
	return systememail.Settings{
		Server:      systememail.Server{Host: "smtp.example.com", Port: 587, Security: systememail.SecuritySTARTTLS, Username: "inbok@example.com"},
		HasPassword: true,
		FromAddress: "inbok@example.com", FromName: "inbok", TestedAt: &systemEmailTested,
	}
}

func adminSessions() *fakeSessions {
	return &fakeSessions{identity: session.Identity{
		User:   account.User{ID: 42, Email: "admin@example.com", Role: account.RoleAdmin},
		Access: session.AccessAdmin,
	}}
}

// requestSystemEmail sends an authenticated request with a same-origin write source.
func requestSystemEmail(api *API, method, path, body string) *httptest.ResponseRecorder {
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

func TestGetSystemEmail(t *testing.T) {
	settings := systemEmailSettingsFixture()
	for _, c := range []struct {
		name     string
		settings *systememail.Settings
		want     string
	}{
		{"not configured", nil, `{"data":{}}`},
		{"without sign-in or test", &systememail.Settings{Server: systememail.Server{Host: "localhost", Port: 1025, Security: systememail.SecurityNone}, FromAddress: "inbok@localhost", FromName: "inbok"},
			`{"data":{"settings":{"host":"localhost","port":1025,"security":"none","username":"","hasPassword":false,` +
				`"fromAddress":"inbok@localhost","fromName":"inbok","testedAt":null}}}`},
		{"configured", &settings, `{"data":{"settings":{"host":"smtp.example.com","port":587,"security":"starttls",` +
			`"username":"inbok@example.com","hasPassword":true,"fromAddress":"inbok@example.com","fromName":"inbok",` +
			`"testedAt":"2026-10-30T12:00:00Z"}}}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeSystemEmail{settings: c.settings}
			api := New(discard, Services{SystemEmail: service, Session: adminSessions()})

			response := requestSystemEmail(api, http.MethodGet, "/system-email", "")

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.JSONEq(t, c.want, response.Body.String())
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		})
	}
}

func TestSaveSystemEmail(t *testing.T) {
	saved := systemEmailSettingsFixture()
	saved.TestedAt = nil
	service := &fakeSystemEmail{saved: saved}
	var logs bytes.Buffer
	api := New(slog.New(slog.NewTextHandler(&logs, nil)), Services{SystemEmail: service, Session: adminSessions()})

	response := requestSystemEmail(api, http.MethodPut, "/system-email", systemEmailBody)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, int64(42), service.actorID, "the actor is the signed-in administrator")
	assert.Equal(t, systememail.Input{
		Server:      systememail.Server{Host: "smtp.example.com", Port: 587, Security: systememail.SecuritySTARTTLS, Username: "inbok@example.com"},
		Password:    "app password 7f3c",
		FromAddress: "inbok@example.com", FromName: "inbok",
	}, service.input)
	assert.JSONEq(t, `{"data":{"settings":{"host":"smtp.example.com","port":587,"security":"starttls",`+
		`"username":"inbok@example.com","hasPassword":true,"fromAddress":"inbok@example.com","fromName":"inbok",`+
		`"testedAt":null}}}`, response.Body.String())
	assert.NotContains(t, logs.String(), "app password")
}

// Username and password are optional, for a server that needs no sign-in.
func TestSaveSystemEmailWithoutSignIn(t *testing.T) {
	service := &fakeSystemEmail{}
	api := New(discard, Services{SystemEmail: service, Session: adminSessions()})

	response := requestSystemEmail(api, http.MethodPut, "/system-email",
		`{"host":"localhost","port":1025,"security":"none","fromAddress":"inbok@localhost","fromName":"inbok"}`)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Empty(t, service.input.Username)
	assert.Empty(t, service.input.Password)
}

// An omitted or empty password reaches the service as empty, which keeps the
// saved one.
func TestSaveSystemEmailKeepsPassword(t *testing.T) {
	for _, c := range []struct{ name, replace string }{
		{"omitted", `,"password":"app password 7f3c"`},
		{"empty", `"password":"app password 7f3c"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeSystemEmail{}
			api := New(discard, Services{SystemEmail: service, Session: adminSessions()})
			with := ""
			if c.name == "empty" {
				with = `"password":""`
			}
			body := strings.Replace(systemEmailBody, c.replace, with, 1)
			require.NotEqual(t, systemEmailBody, body)

			response := requestSystemEmail(api, http.MethodPut, "/system-email", body)

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.Equal(t, "inbok@example.com", service.input.Username)
			assert.Empty(t, service.input.Password)
		})
	}
}

// Surrounding whitespace, which Huma accepts, is trimmed from the host,
// username, and From address before the service sees them.
func TestSaveSystemEmailTrims(t *testing.T) {
	service := &fakeSystemEmail{}
	api := New(discard, Services{SystemEmail: service, Session: adminSessions()})
	body := strings.NewReplacer(`"host":"smtp.example.com"`, `"host":" smtp.example.com\r\n"`,
		`"username":"inbok@example.com"`, `"username":" inbok@example.com\t"`,
		`"fromAddress":"inbok@example.com"`, `"fromAddress":"\t inbok@example.com "`).Replace(systemEmailBody)

	response := requestSystemEmail(api, http.MethodPut, "/system-email", body)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "smtp.example.com", service.input.Host)
	assert.Equal(t, "inbok@example.com", service.input.Username)
	assert.Equal(t, "inbok@example.com", service.input.FromAddress)
}

// A username of only spaces means no sign-in.
func TestSaveSystemEmailBlankUsername(t *testing.T) {
	service := &fakeSystemEmail{}
	api := New(discard, Services{SystemEmail: service, Session: adminSessions()})
	body := strings.Replace(systemEmailBody, `"username":"inbok@example.com"`, `"username":"   "`, 1)

	response := requestSystemEmail(api, http.MethodPut, "/system-email", body)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Empty(t, service.input.Username)
}

// Inputs that break the contract are refused before the service, at their field.
func TestSaveSystemEmailInvalid(t *testing.T) {
	for _, c := range []struct {
		name, replace, with, location, message string
	}{
		{"blank host", `"host":"smtp.example.com"`, `"host":"  "`, "body.host", "Enter a valid value."},
		{"long host", `"host":"smtp.example.com"`, `"host":"` + strings.Repeat("a", 254) + `"`, "body.host", "Use at most 253 characters."},
		{"port zero", `"port":587`, `"port":0`, "body.port", "Enter a whole number from 1 to 65535."},
		{"unknown security", `"security":"starttls"`, `"security":"ssl"`, "body.security", "Enter a valid value."},
		{"from address", `"fromAddress":"inbok@example.com"`, `"fromAddress":"Inbok <inbok@example.com>"`, "body.fromAddress", "Enter a valid value."},
		{"blank from name", `"fromName":"inbok"`, `"fromName":""`, "body.fromName", "Enter a valid value."},
		{"long password", `"password":"app password 7f3c"`, `"password":"` + strings.Repeat("p", 256) + `"`, "body.password", "Use at most 255 characters."},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeSystemEmail{}
			api := New(discard, Services{SystemEmail: service, Session: adminSessions()})
			body := strings.Replace(systemEmailBody, c.replace, c.with, 1)
			require.NotEqual(t, systemEmailBody, body)

			response := requestSystemEmail(api, http.MethodPut, "/system-email", body)

			require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			assert.Zero(t, service.calls)
			problem := decodeJSON(t, response.Body.Bytes())
			assert.Equal(t, problemPrefix+"validation-error", problem["type"])
			assert.Equal(t, []any{map[string]any{"location": c.location, "message": c.message}}, problem["errors"])
		})
	}
}

// The service's rules that tie fields together are field errors too.
func TestSaveSystemEmailRejected(t *testing.T) {
	for _, c := range []struct {
		err               error
		location, message string
	}{
		{systememail.ErrCleartextUsername, "body.security", "Choose TLS or STARTTLS to sign in with a username."},
		{systememail.ErrPasswordWithoutUsername, "body.username", "Enter the username for this password."},
		{systememail.ErrPasswordRequired, "body.password", "Enter the password for this server and username."},
		{systememail.ErrInvalidHost, "body.host", "Enter a host name without control characters."},
		{systememail.ErrInvalidUsername, "body.username", "Enter a username without control characters."},
		{systememail.ErrInvalidPassword, "body.password", "Enter a password of at most 255 bytes, without control characters."},
		{systememail.ErrInvalidFromName, "body.fromName", "Enter a name without control characters."},
	} {
		t.Run(c.err.Error(), func(t *testing.T) {
			api := New(discard, Services{SystemEmail: &fakeSystemEmail{err: c.err}, Session: adminSessions()})

			response := requestSystemEmail(api, http.MethodPut, "/system-email", systemEmailBody)

			require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			problem := decodeJSON(t, response.Body.Bytes())
			assert.Equal(t, problemPrefix+"validation-error", problem["type"])
			assert.Equal(t, []any{map[string]any{"location": c.location, "message": c.message}}, problem["errors"])
		})
	}
}

func TestSendSystemEmailTestMessage(t *testing.T) {
	for _, c := range []struct {
		name   string
		result systememail.TestMessageResult
		want   string
	}{
		{"accepted", systememail.TestMessageResult{Accepted: true}, `{"data":{"status":"accepted"}}`},
		{"failed", systememail.TestMessageResult{Reason: systememail.ReasonAuth}, `{"data":{"status":"failed","reason":"auth"}}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeTestMessages{result: c.result}
			api := New(discard, Services{TestMessages: service, Session: adminSessions()})

			response := requestSystemEmail(api, http.MethodPost, "/system-email/test-message", `{"recipient":" admin@example.org "}`)

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.JSONEq(t, c.want, response.Body.String())
			assert.Equal(t, "admin@example.org", service.recipient, "surrounding spaces are trimmed")
		})
	}
}

func TestSendSystemEmailTestMessageInvalidRecipient(t *testing.T) {
	for _, recipient := range []string{"", "admin", "Admin <admin@example.org>", "admin@example.org, other@example.org"} {
		t.Run(recipient, func(t *testing.T) {
			service := &fakeTestMessages{}
			api := New(discard, Services{TestMessages: service, Session: adminSessions()})

			response := requestSystemEmail(api, http.MethodPost, "/system-email/test-message", `{"recipient":"`+recipient+`"}`)

			require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			assert.Zero(t, service.calls)
			problem := decodeJSON(t, response.Body.Bytes())
			assert.Equal(t, []any{map[string]any{"location": "body.recipient", "message": "Enter a valid value."}}, problem["errors"])
		})
	}
}

func TestSendSystemEmailTestMessageNotConfigured(t *testing.T) {
	api := New(discard, Services{TestMessages: &fakeTestMessages{err: systememail.ErrNotConfigured}, Session: adminSessions()})

	response := requestSystemEmail(api, http.MethodPost, "/system-email/test-message", `{"recipient":"admin@example.org"}`)

	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	problem := decodeJSON(t, response.Body.Bytes())
	assert.Equal(t, problemPrefix+"system-email-not-configured", problem["type"])
	assert.Equal(t, "Save the system email settings before sending a test message.", problem["detail"])
}

// Unexpected failures are a fixed 500 with the cause only in the log.
func TestSystemEmailUnexpectedFailure(t *testing.T) {
	failure := errors.New("private storage cause")
	for _, c := range []struct {
		name, method, path, body string
	}{
		{"get", http.MethodGet, "/system-email", ""},
		{"save", http.MethodPut, "/system-email", systemEmailBody},
		{"test", http.MethodPost, "/system-email/test-message", `{"recipient":"admin@example.org"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			var logs bytes.Buffer
			api := New(slog.New(slog.NewTextHandler(&logs, nil)), Services{
				SystemEmail: &fakeSystemEmail{err: failure}, TestMessages: &fakeTestMessages{err: failure}, Session: adminSessions(),
			})

			response := requestSystemEmail(api, c.method, c.path, c.body)

			require.Equal(t, http.StatusInternalServerError, response.Code)
			assert.NotContains(t, response.Body.String(), "private storage cause")
			assert.Contains(t, logs.String(), "private storage cause")
			assert.NotContains(t, logs.String(), "app password")
		})
	}
}

// Every operation needs admin access: members and administrators without
// TOTP are refused before any service runs.
func TestSystemEmailAccess(t *testing.T) {
	member := &fakeSessions{identity: session.Identity{User: account.User{ID: 7, Role: account.RoleMember}, Access: session.AccessMember}}
	pending := &fakeSessions{identity: session.Identity{User: account.User{ID: 42, Role: account.RoleAdmin}, Access: session.AccessMember, TOTPRequired: true}}
	for _, op := range []struct {
		name, method, path, body string
	}{
		{"get", http.MethodGet, "/system-email", ""},
		{"save", http.MethodPut, "/system-email", systemEmailBody},
		{"test", http.MethodPost, "/system-email/test-message", `{"recipient":"admin@example.org"}`},
	} {
		for _, c := range []struct {
			name     string
			sessions *fakeSessions
			code     int
			typ      string
		}{
			{"member", member, http.StatusForbidden, "forbidden"},
			{"admin without TOTP", pending, http.StatusForbidden, "totp-required"},
			{"signed out", &fakeSessions{currentErr: session.ErrUnauthenticated}, http.StatusUnauthorized, "unauthenticated"},
		} {
			t.Run(op.name+" "+c.name, func(t *testing.T) {
				settings, messages := &fakeSystemEmail{}, &fakeTestMessages{}
				api := New(discard, Services{SystemEmail: settings, TestMessages: messages, Session: c.sessions})

				response := requestSystemEmail(api, op.method, op.path, op.body)

				require.Equal(t, c.code, response.Code, response.Body.String())
				assert.Equal(t, problemPrefix+c.typ, decodeJSON(t, response.Body.Bytes())["type"])
				assert.Zero(t, settings.calls)
				assert.Zero(t, messages.calls)
			})
		}
	}
}

func TestSystemEmailOpenAPI(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()
	for _, op := range []struct {
		operation *huma.Operation
		id        string
		statuses  []string
	}{
		{doc.Paths["/system-email"].Get, "getSystemEmail", []string{"200", "401", "403", "default"}},
		{doc.Paths["/system-email"].Put, "saveSystemEmail", []string{"200", "401", "403", "422", "default"}},
		{doc.Paths["/system-email/test-message"].Post, "sendSystemEmailTestMessage", []string{"200", "401", "403", "409", "422", "default"}},
	} {
		operation := op.operation
		require.NotNil(t, operation, op.id)
		assert.Equal(t, op.id, operation.OperationID)
		assert.Equal(t, session.AccessAdmin, operation.Metadata[accessKey], op.id)
		var statuses []string
		for status := range operation.Responses {
			statuses = append(statuses, status)
		}
		assert.ElementsMatch(t, op.statuses, statuses, op.id)
	}
	forbidden := doc.Paths["/system-email"].Get.Responses["403"].Content[problemContentType].Examples
	assert.Equal(t, forbiddenProblem(), forbidden["forbidden"].Value)
	assert.Equal(t, totpRequiredProblem(), forbidden["totpRequired"].Value)
	schemas := doc.Components.Schemas.Map()
	require.NotNil(t, schemas["NewSystemEmailSettings"])
	assert.True(t, schemas["NewSystemEmailSettings"].Properties["password"].WriteOnly)
	require.NotNil(t, schemas["SystemEmailSettings"])
	assert.NotContains(t, schemas["SystemEmailSettings"].Properties, "password")
	require.NotNil(t, schemas["SystemEmailTestMessageResult"])
	assert.Equal(t, []string{"status"}, schemas["SystemEmailTestMessageResult"].Required, "reason is only for failures")
}
