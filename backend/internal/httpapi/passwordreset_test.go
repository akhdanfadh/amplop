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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/attemptlimit"
	"inbok/internal/emailaddress"
	"inbok/internal/passwordreset"
	"inbok/internal/session"
)

// resetRequestMemory keeps accounts and the links sent to them for the real
// passwordreset.Requests.
type resetRequestMemory struct {
	mu        sync.Mutex
	accounts  map[string]passwordreset.Account
	linksSent map[int64][]time.Time
}

// RecordPasswordResetRequest accepts every request.
func (m *resetRequestMemory) RecordPasswordResetRequest(_ context.Context, _ string, _ time.Time, check func([]time.Time) (time.Time, error)) (int64, error) {
	_, err := check(nil)
	return 1, err
}

// CreatePasswordResetLink calls create with the account with email and the
// links sent to it.
func (m *resetRequestMemory) CreatePasswordResetLink(_ context.Context, _ int64, email string, _ time.Time, create func(passwordreset.LinkState) (passwordreset.LinkChange, error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var state passwordreset.LinkState
	if found, ok := m.accounts[email]; ok {
		state = passwordreset.LinkState{Account: &found, LinksSent: m.linksSent[found.User.ID]}
	}
	_, err := create(state)
	return err
}

// An unknown email, an unverified one, a deactivated account, one over its
// limit, and one that gets a link all get the same response, so it does not
// tell which one was asked about.
func TestRequestPasswordResetLinkSameResponse(t *testing.T) {
	// Given one account of each kind, and one with no account at all.
	now := time.Now()
	user := func(id int64, email string) account.User {
		return account.User{ID: id, Email: email, Role: account.RoleMember}
	}
	storage := &resetRequestMemory{
		accounts: map[string]passwordreset.Account{
			"member@example.org":      {User: user(1, "member@example.org"), Verified: true},
			"unverified@example.org":  {User: user(2, "unverified@example.org")},
			"deactivated@example.org": {User: user(3, "deactivated@example.org"), Verified: true, Deactivated: true},
			"busy@example.org":        {User: user(4, "busy@example.org"), Verified: true},
		},
		linksSent: map[int64][]time.Time{4: {now, now, now}},
	}
	mailer, background := &recordingMailer{}, passwordreset.NewBackground(slog.New(slog.DiscardHandler))
	requests := passwordreset.NewRequests(storage, mailer, &url.URL{Scheme: "https", Host: "mail.example.com"}, background, time.Now)
	api := New(discard, Services{PasswordResetRequests: requests})

	// When each is asked about.
	var responses []*httptest.ResponseRecorder
	for _, email := range []string{"nobody@example.org", "unverified@example.org", "deactivated@example.org", "busy@example.org", "member@example.org"} {
		responses = append(responses, postPublic(api, passwordResetLinksPath, `{"email":"`+email+`"}`))
	}
	require.NoError(t, background.Wait(t.Context()))

	// Then only the eligible account got a link, and every response is the
	// same.
	assert.Equal(t, []string{"Reset your inbok password"}, mailer.subjects)
	assert.Equal(t, []string{"member@example.org"}, mailer.recipients)
	for _, response := range responses {
		assert.Equal(t, http.StatusAccepted, response.Code)
		assert.JSONEq(t, `{"data":{"status":"requested"}}`, response.Body.String())
		assert.Equal(t, headersBut(responses[0], requestIDHeader), headersBut(response, requestIDHeader))
		assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	}
}

type fakeResetRequests struct {
	err    error
	email  string
	client netip.Addr
}

// Request records email and client and returns err.
func (f *fakeResetRequests) Request(_ context.Context, email string, client netip.Addr) error {
	f.email, f.client = email, client
	return f.err
}

func TestRequestPasswordResetLink(t *testing.T) {
	service := &fakeResetRequests{}
	api := New(discard, Services{PasswordResetRequests: service})

	response := postPublic(api, passwordResetLinksPath, `{"email":"Member@Example.org"}`)

	require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
	// The service parses the email; the client address comes from Gin.
	assert.Equal(t, "Member@Example.org", service.email)
	assert.Equal(t, netip.MustParseAddr("192.0.2.1"), service.client)
}

func TestRequestPasswordResetLinkProblems(t *testing.T) {
	for _, c := range []struct {
		name       string
		err        error
		status     int
		typ        string
		retryAfter string
		location   string
	}{
		{"invalid email", emailaddress.ErrInvalid, http.StatusUnprocessableEntity, "validation-error", "", "body.email"},
		{"throttled", &attemptlimit.Error{Err: passwordreset.ErrThrottled, Wait: 90 * time.Second}, http.StatusTooManyRequests, "password-reset-throttled", "90", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			api := New(discard, Services{PasswordResetRequests: &fakeResetRequests{err: c.err}})

			response := postPublic(api, passwordResetLinksPath, `{"email":"member@example.org"}`)

			require.Equal(t, c.status, response.Code, response.Body.String())
			body := decodeJSON(t, response.Body.Bytes())
			assert.Equal(t, problemPrefix+c.typ, body["type"])
			assert.Equal(t, c.retryAfter, response.Header().Get(retryAfterHeader))
			assert.Equal(t, c.location, inputLocation(body))
		})
	}
}

func TestRedeemPasswordResetLink(t *testing.T) {
	service := &fakeCompletion{user: account.User{ID: 7, Email: "member@example.org", Role: account.RoleMember}}
	api := New(discard, Services{PasswordResetCompletion: service})

	response := postPublic(api, passwordResetRedeemPath, resetBody)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"email":"member@example.org"}}`, response.Body.String())
	// The service parses and compares the email.
	assert.Equal(t, []string{"link-secret", "Member@Example.org", "correct horse battery"}, []string{service.secret, service.email, service.password})
	// No one is signed in.
	assert.Empty(t, response.Header().Values("Set-Cookie"))
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
}

func TestRedeemPasswordResetLinkProblems(t *testing.T) {
	for _, c := range []struct {
		name     string
		err      error
		status   int
		typ      string
		location string
	}{
		{"password", account.ErrPasswordInvalid, http.StatusUnprocessableEntity, "validation-error", "body.password"},
		{"invalid email", emailaddress.ErrInvalid, http.StatusUnprocessableEntity, "validation-error", "body.email"},
		{"email mismatch", passwordreset.ErrEmailMismatch, http.StatusUnprocessableEntity, "validation-error", "body.email"},
		{"link invalid", passwordreset.ErrLinkInvalid, http.StatusNotFound, "password-reset-link-invalid", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			api := New(discard, Services{PasswordResetCompletion: &fakeCompletion{err: c.err}})

			response := postPublic(api, passwordResetRedeemPath, resetBody)

			require.Equal(t, c.status, response.Code, response.Body.String())
			body := decodeJSON(t, response.Body.Bytes())
			assert.Equal(t, problemPrefix+c.typ, body["type"])
			assert.Equal(t, c.location, inputLocation(body))
		})
	}
}

const resetBody = `{"secret":"link-secret","email":"Member@Example.org","password":"correct horse battery"}`

// An unexpected failure becomes a fixed 500 whose cause is only logged.
func TestPasswordResetUnexpectedFailure(t *testing.T) {
	failure := errors.New("private storage cause")
	for _, c := range []struct {
		name, path, body string
	}{
		{"request", passwordResetLinksPath, `{"email":"member@example.org"}`},
		{"redeem", passwordResetRedeemPath, resetBody},
	} {
		t.Run(c.name, func(t *testing.T) {
			var logs bytes.Buffer
			api := New(slog.New(slog.NewTextHandler(&logs, nil)), Services{
				PasswordResetRequests:   &fakeResetRequests{err: failure},
				PasswordResetCompletion: &fakeCompletion{err: failure},
			})

			response := postPublic(api, c.path, c.body)

			require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
			assert.NotContains(t, response.Body.String(), "private storage cause")
			assert.Contains(t, logs.String(), "private storage cause")
		})
	}
}

// Huma refuses a password outside the rule before the service hashes it.
func TestRedeemPasswordResetLinkShortPassword(t *testing.T) {
	service := &fakeCompletion{}
	api := New(discard, Services{PasswordResetCompletion: service})

	response := postPublic(api, passwordResetRedeemPath, `{"secret":"link-secret","email":"member@example.org","password":"too short"}`)

	require.Equal(t, http.StatusUnprocessableEntity, response.Code)
	assert.Contains(t, response.Body.String(), `"location":"body.password"`)
	assert.Zero(t, service.calls)
}

// Keep the HTTP contract's literal tags tied to the password rule.
func TestRedeemPasswordResetLinkPasswordRule(t *testing.T) {
	field, ok := reflect.TypeFor[newPasswordFromLink]().FieldByName("Password")
	require.True(t, ok)
	assert.Equal(t, strconv.Itoa(account.PasswordMinLength), field.Tag.Get("minLength"))
	assert.Equal(t, strconv.Itoa(account.PasswordMaxLength), field.Tag.Get("maxLength"))
}

func TestPasswordResetOpenAPI(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()

	for _, c := range []struct {
		path     string
		id       string
		statuses []string
	}{
		{passwordResetLinksPath, "requestPasswordResetLink", []string{"202", "422", "429", "default"}},
		{passwordResetRedeemPath, "redeemPasswordResetLink", []string{"200", "404", "422", "429", "default"}},
	} {
		item := doc.Paths[c.path]
		require.NotNil(t, item, c.path)
		operation := item.Post
		require.NotNil(t, operation, c.id)
		assert.Equal(t, c.id, operation.OperationID)
		assert.Equal(t, session.AccessPublic, operation.Metadata[accessKey], c.id)
		assert.Empty(t, operation.Security, c.id)
		assert.ElementsMatch(t, c.statuses, slices.Collect(maps.Keys(operation.Responses)), c.id)
	}
	// A 202 has a body, which the web's transport needs.
	accepted := doc.Paths[passwordResetLinksPath].Post.Responses["202"]
	require.NotNil(t, accepted.Content["application/json"])
	assert.True(t, doc.Components.Schemas.Map()["NewPasswordFromLink"].Properties["secret"].WriteOnly)
	assert.Contains(t, doc.Paths[passwordResetRedeemPath].Post.Responses["422"].Content[problemContentType].Examples, "emailMismatch")
}

// headersBut returns response's headers without name, which differs per
// request.
func headersBut(response *httptest.ResponseRecorder, name string) http.Header {
	headers := response.Header().Clone()
	headers.Del(name)
	return headers
}
