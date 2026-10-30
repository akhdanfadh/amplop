package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/session"
	"inbok/internal/totp"
)

type fakeCodeSignIn struct {
	issued       session.Issued
	err          error
	secret, code string
	device       session.Device
	calls        int
}

func (f *fakeCodeSignIn) Complete(_ context.Context, secret, code string, device session.Device) (session.Issued, error) {
	f.calls++
	f.secret, f.code, f.device = secret, code, device
	return f.issued, f.err
}

func postCode(api *API, body, origin string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v0/sessions/totp", strings.NewReader(body))
	request.Header.Set("Content-Type", jsonContentType)
	request.Header.Set("Origin", origin)
	request.Header.Set("User-Agent", "inbok-code-test/1.0")
	request.AddCookie(&http.Cookie{Name: challengeCookieName, Value: "private-challenge"})

	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)

	return response
}

func TestCreateSessionWithCode(t *testing.T) {
	service := &fakeCodeSignIn{issued: session.Issued{
		Secret: "private-session",
		State:  session.State{ExpiresAt: time.Now().Add(time.Hour)},
	}}
	var logs bytes.Buffer
	api := New(slog.New(slog.NewTextHandler(&logs, nil)), Services{CodeSignIn: service})

	response := postCode(api, `{"code":"012345"}`, "http://example.com")

	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"step":"complete"}}`, response.Body.String())
	assert.Equal(t, "012345", service.code)
	assert.Equal(t, "private-challenge", service.secret)
	assert.Equal(t, session.Device{UserAgent: "inbok-code-test/1.0", Addr: netip.MustParseAddr("192.0.2.1")}, service.device)
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))

	cookies := response.Result().Cookies()
	require.Len(t, cookies, 2)
	assert.Equal(t, sessionCookieName, cookies[0].Name)
	assert.Equal(t, service.issued.Secret, cookies[0].Value)
	assert.True(t, cookies[0].Secure)
	assert.True(t, cookies[0].HttpOnly)
	assert.Equal(t, "/", cookies[0].Path)
	assert.Empty(t, cookies[0].Domain)
	assert.Equal(t, http.SameSiteLaxMode, cookies[0].SameSite)
	assert.Equal(t, challengeCookieName, cookies[1].Name)
	assert.Negative(t, cookies[1].MaxAge)

	for _, secret := range []string{"012345", "private-challenge", "private-session"} {
		assert.NotContains(t, response.Body.String(), secret)
		assert.NotContains(t, logs.String(), secret)
	}
}

func TestCodeSessionProblems(t *testing.T) {
	for _, c := range []struct {
		name    string
		err     error
		status  int
		problem string
	}{
		{"invalid code", totp.ErrInvalidCode, http.StatusUnprocessableEntity, "invalid-sign-in-code"},
		{"invalid challenge", session.ErrInvalidChallenge, http.StatusUnauthorized, "sign-in-challenge-invalid"},
		{"throttled", totp.ErrThrottled, http.StatusTooManyRequests, "totp-throttled"},
		{"commit failure", errors.New("private storage failure"), http.StatusInternalServerError, "internal-error"},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeCodeSignIn{err: c.err, issued: session.Issued{Secret: "must-not-issue"}}
			api := New(discard, Services{CodeSignIn: service})

			response := postCode(api, `{"code":"012345"}`, "http://example.com")

			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), `"type":"/api/v0/problems/`+c.problem+`"`)
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			assert.NotContains(t, response.Body.String(), "private storage failure")
			assert.NotContains(t, response.Body.String(), "must-not-issue")
			assert.NotContains(t, response.Body.String(), "012345")
			cookies := response.Result().Cookies()
			if c.err == session.ErrInvalidChallenge {
				require.Len(t, cookies, 1)
				assert.Equal(t, challengeCookieName, cookies[0].Name)
				assert.Negative(t, cookies[0].MaxAge)
				return
			}
			assert.Empty(t, cookies, "preserve retryable challenges on code, throttle, or storage errors")
		})
	}
}

func TestCodeSessionRequestGate(t *testing.T) {
	for _, c := range []struct {
		name, body, origin string
		status             int
	}{
		{"cross origin", `{"code":"012345"}`, "https://untrusted.example", http.StatusForbidden},
		{"missing code", `{}`, "http://example.com", http.StatusUnprocessableEntity},
		{"wrong type", `{"code":123456}`, "http://example.com", http.StatusUnprocessableEntity},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeCodeSignIn{}
			response := postCode(New(discard, Services{CodeSignIn: service}), c.body, c.origin)
			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Zero(t, service.calls)
			assert.NotContains(t, response.Body.String(), "123456")
		})
	}
}

func TestCodeSessionOpenAPI(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()
	path := doc.Paths["/sessions/totp"]
	require.NotNil(t, path)
	require.NotNil(t, path.Post)
	assert.Equal(t, session.AccessPublic, path.Post.Metadata[accessKey])
	assert.Equal(t, []map[string][]string{{challengeSecuritySchemeName: {}}}, path.Post.Security)
	assert.Empty(t, path.Post.Parameters, "the browser supplies the HttpOnly cookie automatically")
	require.NotNil(t, path.Post.Responses["201"].Headers["Set-Cookie"])
}
