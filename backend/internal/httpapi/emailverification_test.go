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

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/emailverification"
	"inbok/internal/session"
	"inbok/internal/systememail"
)

// fakeEmailVerification records each call and answers with address and err.
type fakeEmailVerification struct {
	address string
	err     error

	calls  int
	userID int64
	secret string
}

func (f *fakeEmailVerification) Request(_ context.Context, userID int64) (string, error) {
	f.calls++
	f.userID = userID
	return f.address, f.err
}

func (f *fakeEmailVerification) Redeem(_ context.Context, secret string) error {
	f.calls++
	f.secret = secret
	return f.err
}

const testVerificationSecret = "Zk3Rr0eY7Hq1Vb2Nc4Xw6Pd8Ls0Ja9Mt5Uo7Ki3Gh1Fe"

// postRedeem sends a same-origin redeem request, with cookie as the session
// cookie when it is not empty.
func postRedeem(api *API, body, cookie string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v0/email-verifications/redeem", strings.NewReader(body))
	request.Header.Set("Origin", "http://"+request.Host)
	request.Header.Set("Content-Type", jsonContentType)
	if cookie != "" {
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	}
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	return response
}

// ========================
// Email verification request
// ========================

// Any signed-in user can ask, including an administrator still enrolling
// TOTP; the link is for the session's own account.
func TestRequestEmailVerification(t *testing.T) {
	for _, role := range []account.Role{account.RoleMember, account.RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			service := &fakeEmailVerification{address: "alice@example.com"}
			api := New(discard, Services{EmailVerification: service, Session: memberSessions(role)})

			response := sendSessionRequest(api, http.MethodPost, "/account/email-verification")

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.JSONEq(t, `{"data":{"address":"alice@example.com"}}`, response.Body.String())
			assert.Equal(t, int64(42), service.userID)
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		})
	}
}

func TestRequestEmailVerificationSignedOut(t *testing.T) {
	service := &fakeEmailVerification{}
	api := New(discard, Services{EmailVerification: service, Session: &fakeSessions{currentErr: session.ErrUnauthenticated}})

	response := sendSessionRequest(api, http.MethodPost, "/account/email-verification")

	require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
	assert.Equal(t, problemPrefix+"unauthenticated", decodeJSON(t, response.Body.Bytes())["type"])
	assert.Zero(t, service.calls)
}

func TestRequestEmailVerificationRefused(t *testing.T) {
	for _, c := range []struct {
		name   string
		err    error
		code   int
		typ    string
		detail string
	}{
		{"already verified", emailverification.ErrAlreadyVerified, http.StatusConflict, "email-already-verified",
			"Your email address is already verified."},
		{"no system email", systememail.ErrNotConfigured, http.StatusConflict, "system-email-not-configured",
			"Email can't be sent until an administrator sets up system email."},
		{"account deleted", account.ErrUserNotFound, http.StatusUnauthorized, "unauthenticated",
			"Sign in to continue."},
		{"throttled", emailverification.ErrThrottled, http.StatusTooManyRequests, "email-verification-throttled",
			"Too many verification links were sent recently. Try again later."},
		{"send failed", &systememail.SendError{Reason: systememail.ReasonRecipient, Err: errors.New("550 private reply")}, http.StatusBadGateway, "email-not-sent",
			"The email could not be sent. Try again later."},
	} {
		t.Run(c.name, func(t *testing.T) {
			api := New(discard, Services{EmailVerification: &fakeEmailVerification{err: c.err}, Session: memberSessions(account.RoleMember)})

			response := sendSessionRequest(api, http.MethodPost, "/account/email-verification")

			require.Equal(t, c.code, response.Code, response.Body.String())
			problem := decodeJSON(t, response.Body.Bytes())
			assert.Equal(t, problemPrefix+c.typ, problem["type"])
			assert.Equal(t, c.detail, problem["detail"])
			assert.NotContains(t, response.Body.String(), "private reply")
		})
	}
}

// ========================
// Email verification redeem
// ========================

// Redeeming ignores any session cookie and signs no one in.
func TestRedeemEmailVerification(t *testing.T) {
	service := &fakeEmailVerification{}
	sessions := &fakeSessions{currentErr: errors.New("redeem must not check a session")}
	api := New(discard, Services{EmailVerification: service, Session: sessions})

	response := postRedeem(api, `{"secret":"`+testVerificationSecret+`"}`, "private-cookie-secret")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"status":"verified"}}`, response.Body.String())
	assert.Equal(t, testVerificationSecret, service.secret)
	assert.Empty(t, response.Header().Values("Set-Cookie"))
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
}

// Every link that cannot verify gets the same answer.
func TestRedeemEmailVerificationInvalid(t *testing.T) {
	api := New(discard, Services{EmailVerification: &fakeEmailVerification{err: emailverification.ErrLinkInvalid}})

	response := postRedeem(api, `{"secret":"not-a-link"}`, "")

	require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
	problem := decodeJSON(t, response.Body.Bytes())
	assert.Equal(t, problemPrefix+"email-verification-link-invalid", problem["type"])
	assert.Equal(t, "This link has expired, was already used, or is not valid. Ask for a new one.", problem["detail"])
}

func TestRedeemEmailVerificationMissingSecret(t *testing.T) {
	service := &fakeEmailVerification{}
	api := New(discard, Services{EmailVerification: service})

	response := postRedeem(api, `{}`, "")

	require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	problem := decodeJSON(t, response.Body.Bytes())
	assert.Equal(t, []any{map[string]any{"location": "body.secret", "message": "This field is required."}}, problem["errors"])
	assert.Zero(t, service.calls)
}

// ========================
// Both operations
// ========================

// Unexpected failures, e.g. a password that no longer decrypts, are a fixed
// 500 with the cause only in the log.
func TestEmailVerificationUnexpectedFailure(t *testing.T) {
	failure := errors.Join(systememail.ErrPasswordUnreadable, errors.New("private cause"))
	for _, c := range []struct {
		name string
		send func(*API) *httptest.ResponseRecorder
	}{
		{"request", func(api *API) *httptest.ResponseRecorder {
			return sendSessionRequest(api, http.MethodPost, "/account/email-verification")
		}},
		{"redeem", func(api *API) *httptest.ResponseRecorder {
			return postRedeem(api, `{"secret":"`+testVerificationSecret+`"}`, "")
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var logs bytes.Buffer
			api := New(slog.New(slog.NewTextHandler(&logs, nil)), Services{
				EmailVerification: &fakeEmailVerification{err: failure}, Session: memberSessions(account.RoleMember),
			})

			response := c.send(api)

			require.Equal(t, http.StatusInternalServerError, response.Code)
			assert.NotContains(t, response.Body.String(), "private cause")
			assert.Contains(t, logs.String(), "private cause")
		})
	}
}

func TestEmailVerificationOpenAPI(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()

	for _, op := range []struct {
		operation *huma.Operation
		id        string
		access    session.Access
		statuses  []string
	}{
		{doc.Paths["/account/email-verification"].Post, "requestEmailVerification", session.AccessMember, []string{"200", "401", "409", "429", "502", "default"}},
		{doc.Paths["/email-verifications/redeem"].Post, "redeemEmailVerification", session.AccessPublic, []string{"200", "404", "422", "default"}},
	} {
		operation := op.operation
		require.NotNil(t, operation, op.id)
		assert.Equal(t, op.id, operation.OperationID)
		assert.Equal(t, op.access, operation.Metadata[accessKey], op.id)
		var statuses []string
		for status := range operation.Responses {
			statuses = append(statuses, status)
		}
		assert.ElementsMatch(t, op.statuses, statuses, op.id)
	}
	assert.Empty(t, doc.Paths["/email-verifications/redeem"].Post.Security, "redeem needs no session")
	conflicts := doc.Paths["/account/email-verification"].Post.Responses["409"].Content[problemContentType].Examples
	assert.Contains(t, conflicts, "alreadyVerified")
	assert.Contains(t, conflicts, "notConfigured")
	schemas := doc.Components.Schemas.Map()
	require.NotNil(t, schemas["EmailVerificationSecret"])
	assert.True(t, schemas["EmailVerificationSecret"].Properties["secret"].WriteOnly)
}
