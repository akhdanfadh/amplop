package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Every response kind, the API's and the web app's, carries the headers,
// including rejected requests.
func TestSecurityHeaders(t *testing.T) {
	cases := []struct {
		method, path string
		sameOrigin   bool
	}{
		{http.MethodGet, "/api/v0/health", false},
		{http.MethodGet, "/api/v0/nope", false},
		{http.MethodGet, "/api/v0/docs", false},
		{http.MethodGet, "/security", false},
		{http.MethodGet, "/assets/index-BrBO-kCw.js", false},
		{http.MethodGet, "/favicon.ico", false},
		// Refused by the cross-origin check, which runs before routing.
		{http.MethodPost, "/api/v0/probe/events", false},
		{http.MethodPost, "/security", false},
		// Refused by the web app as a 405.
		{http.MethodPost, "/security", true},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			request := httptest.NewRequest(c.method, c.path, nil)
			if c.sameOrigin {
				request.Header.Set("Origin", "http://"+request.Host)
			}
			response := httptest.NewRecorder()

			newWebAppHandler(t, &fakeWebApp{}).ServeHTTP(response, request)

			assert.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
			assert.Equal(t, "no-referrer", response.Header().Get("Referrer-Policy"))
			assert.Equal(t, "DENY", response.Header().Get("X-Frame-Options"))
		})
	}
}
