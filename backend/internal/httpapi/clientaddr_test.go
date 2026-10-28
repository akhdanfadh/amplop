package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
