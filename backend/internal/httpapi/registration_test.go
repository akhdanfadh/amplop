package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/attemptlimit"
	"inbok/internal/emailaddress"
	"inbok/internal/registration"
	"inbok/internal/session"
	"inbok/internal/systememail"
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
		{"request", http.MethodPost, registrationLinksPath, `{"email":"visitor@example.org"}`},
		{"redeem", http.MethodPost, registrationRedeemPath, completeBody},
	} {
		t.Run(c.name, func(t *testing.T) {
			var logs bytes.Buffer
			api := New(slog.New(slog.NewTextHandler(&logs, nil)), Services{
				Registration:           &fakeRegistration{err: failure},
				RegistrationRequests:   &fakeRegistrationLinks{err: failure},
				RegistrationCompletion: &fakeCompletion{err: failure},
				Session:                adminSessions(),
			})

			response := requestAuthenticated(api, c.method, c.path, c.body)

			require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
			assert.NotContains(t, response.Body.String(), "private storage cause")
			assert.Contains(t, logs.String(), "private storage cause")
		})
	}
}

// postPublic posts body to path with a same-origin write source and no
// session.
func postPublic(api *API, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, apiPrefix+path, strings.NewReader(body))
	request.Header.Set("Origin", "http://"+request.Host)
	request.Header.Set("Content-Type", jsonContentType)

	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	return response
}

// requestMemory keeps registration attempts in memory for the real
// registration.Requests.
type requestMemory struct {
	accounts []string // emails that have an account
	attempts []registration.Attempt
}

func (m *requestMemory) ReadRegistration(context.Context) (registration.Record, error) {
	return registration.Record{State: registration.StateOpen, SystemEmailTested: true}, nil
}

func (m *requestMemory) RequestRegistration(ctx context.Context, email string, _ time.Time, create func(registration.RequestState) (registration.RequestChange, error)) error {
	record, _ := m.ReadRegistration(ctx)
	change, err := create(registration.RequestState{Registration: record, AccountExists: slices.Contains(m.accounts, email), Attempts: m.attempts})
	if err != nil {
		return err
	}
	m.attempts = append(m.attempts, change.Attempt)
	return nil
}

// recordingMailer records the subjects and recipients of the messages it
// sends, which may come from background work.
type recordingMailer struct {
	mu         sync.Mutex
	subjects   []string
	recipients []string
}

// Send records outgoing's subject and recipient and returns revision 1.
func (m *recordingMailer) Send(_ context.Context, outgoing systememail.Outgoing) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subjects = append(m.subjects, outgoing.Subject)
	m.recipients = append(m.recipients, outgoing.To)
	return 1, nil
}

// A new email, an email with an account, and an email over its limit get the
// same response, so it does not tell which one was asked about.
func TestRequestRegistrationLinkSameResponse(t *testing.T) {
	storage := &requestMemory{accounts: []string{"member@example.org"}}
	for range 3 {
		storage.attempts = append(storage.attempts, registration.Attempt{
			Email: "busy@example.org", ClientPrefix: "198.51.100.1/32", Outcome: registration.OutcomeLink, CreatedAt: time.Now(),
		})
	}
	mailer := &recordingMailer{}
	requests := registration.NewRequests(storage, mailer, &url.URL{Scheme: "https", Host: "mail.example.com"}, time.Now, discard)
	api := New(discard, Services{RegistrationRequests: requests})

	var responses []*httptest.ResponseRecorder
	for _, email := range []string{"visitor@example.org", "member@example.org", "busy@example.org"} {
		responses = append(responses, postPublic(api, registrationLinksPath, `{"email":"`+email+`"}`))
	}

	// A link, a notice, and nothing were sent.
	assert.Equal(t, []string{"Create your inbok account", "Your email address already has an inbok account"}, mailer.subjects)
	for _, response := range responses {
		assert.Equal(t, http.StatusOK, response.Code)
		assert.JSONEq(t, `{"data":{"status":"requested"}}`, response.Body.String())
		assert.Equal(t, responses[0].Header().Get("Content-Type"), response.Header().Get("Content-Type"))
		assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	}
}

type fakeRegistrationLinks struct {
	err    error
	email  string
	client netip.Addr
}

func (f *fakeRegistrationLinks) Send(_ context.Context, email string, client netip.Addr) error {
	f.email, f.client = email, client
	return f.err
}

func TestRequestRegistrationLink(t *testing.T) {
	service := &fakeRegistrationLinks{}
	api := New(discard, Services{RegistrationRequests: service})

	response := postPublic(api, registrationLinksPath, `{"email":"Visitor@Example.org"}`)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	// The service parses the email; the client address comes from Gin.
	assert.Equal(t, "Visitor@Example.org", service.email)
	assert.Equal(t, netip.MustParseAddr("192.0.2.1"), service.client)
}

func TestRequestRegistrationLinkProblems(t *testing.T) {
	sendFailure := &systememail.SendError{Reason: systememail.ReasonAuth, Err: errors.New("535 bad password for kate@mail.example.com")}
	for _, c := range []struct {
		name       string
		err        error
		status     int
		typ        string
		retryAfter string
		location   string
	}{
		{"invalid email", emailaddress.ErrInvalid, http.StatusUnprocessableEntity, "validation-error", "", "body.email"},
		{"closed", registration.ErrClosed, http.StatusConflict, "registration-closed", "", ""},
		{"throttled", &attemptlimit.Error{Err: registration.ErrThrottled, Wait: 90 * time.Second}, http.StatusTooManyRequests, "registration-throttled", "90", ""},
		{"send failed", sendFailure, http.StatusBadGateway, "email-not-sent", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			api := New(discard, Services{RegistrationRequests: &fakeRegistrationLinks{err: c.err}})

			response := postPublic(api, registrationLinksPath, `{"email":"visitor@example.org"}`)

			require.Equal(t, c.status, response.Code, response.Body.String())
			body := decodeJSON(t, response.Body.Bytes())
			assert.Equal(t, problemPrefix+c.typ, body["type"])
			assert.Equal(t, c.retryAfter, response.Header().Get(retryAfterHeader))
			assert.Equal(t, c.location, inputLocation(body))
			// The mail server's reply stays out of the response.
			assert.NotContains(t, response.Body.String(), "kate")
		})
	}
}

type fakeCompletion struct {
	user                    account.User
	err                     error
	calls                   int
	secret, email, password string
}

// Complete records what it was given and returns user with err.
func (f *fakeCompletion) Complete(_ context.Context, secret, email, password string) (account.User, error) {
	f.calls++
	f.secret, f.email, f.password = secret, email, password
	return f.user, f.err
}

const completeBody = `{"secret":"link-secret","email":"Visitor@Example.org","password":"correct horse battery"}`

func TestCompleteRegistration(t *testing.T) {
	service := &fakeCompletion{user: account.User{ID: 7, Email: "visitor@example.org", Role: account.RoleMember}}
	api := New(discard, Services{RegistrationCompletion: service})

	response := postPublic(api, registrationRedeemPath, completeBody)

	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"id":7,"email":"visitor@example.org"}}`, response.Body.String())
	// The service parses and compares the email.
	assert.Equal(t, []string{"link-secret", "Visitor@Example.org", "correct horse battery"}, []string{service.secret, service.email, service.password})
	// No one is signed in.
	assert.Empty(t, response.Header().Values("Set-Cookie"))
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
}

func TestCompleteRegistrationProblems(t *testing.T) {
	for _, c := range []struct {
		name     string
		err      error
		status   int
		typ      string
		location string
	}{
		{"password", account.ErrPasswordInvalid, http.StatusUnprocessableEntity, "validation-error", "body.password"},
		{"invalid email", emailaddress.ErrInvalid, http.StatusUnprocessableEntity, "validation-error", "body.email"},
		{"email mismatch", registration.ErrEmailMismatch, http.StatusUnprocessableEntity, "validation-error", "body.email"},
		{"link invalid", registration.ErrLinkInvalid, http.StatusNotFound, "registration-link-invalid", ""},
		{"closed", registration.ErrClosed, http.StatusConflict, "registration-closed", ""},
		{"account exists", registration.ErrAccountExists, http.StatusConflict, "registration-account-exists", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			api := New(discard, Services{RegistrationCompletion: &fakeCompletion{err: c.err}})

			response := postPublic(api, registrationRedeemPath, completeBody)

			require.Equal(t, c.status, response.Code, response.Body.String())
			body := decodeJSON(t, response.Body.Bytes())
			assert.Equal(t, problemPrefix+c.typ, body["type"])
			assert.Equal(t, c.location, inputLocation(body))
		})
	}
}

// Huma refuses a password outside the rule before the service hashes it.
func TestCompleteRegistrationShortPassword(t *testing.T) {
	service := &fakeCompletion{}
	api := New(discard, Services{RegistrationCompletion: service})

	response := postPublic(api, registrationRedeemPath, `{"secret":"link-secret","email":"visitor@example.org","password":"too short"}`)

	require.Equal(t, http.StatusUnprocessableEntity, response.Code)
	assert.Contains(t, response.Body.String(), `"location":"body.password"`)
	assert.Zero(t, service.calls)
}

// Keep the HTTP contract's literal tags tied to the password rule.
func TestCompleteRegistrationPasswordRule(t *testing.T) {
	field, ok := reflect.TypeFor[newMember]().FieldByName("Password")
	require.True(t, ok)
	assert.Equal(t, strconv.Itoa(account.PasswordMinLength), field.Tag.Get("minLength"))
	assert.Equal(t, strconv.Itoa(account.PasswordMaxLength), field.Tag.Get("maxLength"))
}

func TestRegistrationLinksOpenAPI(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()

	for _, c := range []struct {
		operation *huma.Operation
		id        string
		statuses  []string
	}{
		{doc.Paths[registrationLinksPath].Post, "requestRegistrationLink", []string{"200", "409", "422", "429", "502", "default"}},
		{doc.Paths[registrationRedeemPath].Post, "redeemRegistrationLink", []string{"201", "404", "409", "422", "429", "default"}},
	} {
		require.NotNil(t, c.operation, c.id)
		assert.Equal(t, c.id, c.operation.OperationID)
		assert.Equal(t, session.AccessPublic, c.operation.Metadata[accessKey], c.id)
		assert.Empty(t, c.operation.Security, c.id)
		assert.ElementsMatch(t, c.statuses, slices.Collect(maps.Keys(c.operation.Responses)), c.id)
	}
	conflicts := doc.Paths[registrationRedeemPath].Post.Responses["409"].Content[problemContentType].Examples
	assert.Contains(t, conflicts, "closed")
	assert.Contains(t, conflicts, "accountExists")
	assert.Contains(t, doc.Paths[registrationRedeemPath].Post.Responses["422"].Content[problemContentType].Examples, "emailMismatch")
	assert.True(t, doc.Components.Schemas.Map()["NewMember"].Properties["secret"].WriteOnly)
}

// inputLocation returns the location of a problem body's only input error,
// or "" without one.
func inputLocation(body map[string]any) string {
	inputs, _ := body["errors"].([]any)
	if len(inputs) != 1 {
		return ""
	}
	input, _ := inputs[0].(map[string]any)
	location, _ := input["location"].(string)
	return location
}
