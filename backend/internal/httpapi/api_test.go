package httpapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// discard silences request logs in tests that do not inspect them.
var discard = slog.New(slog.DiscardHandler)

func TestAPIDocs(t *testing.T) {
	router := New(discard, Services{}).Handler()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v0/docs", nil))

	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), "/api/v0/openapi.yaml", "documentation must load the versioned OpenAPI document")
}

func TestOpenAPI(t *testing.T) {
	a := New(discard, Services{})
	doc, err := a.OpenAPI()
	require.NoError(t, err)

	assert.True(t, bytes.HasSuffix(doc, []byte("}\n")), "document must end with a newline")

	// The exported document must match the one the server serves.
	response := httptest.NewRecorder()
	a.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v0/openapi.json", nil))

	assert.JSONEq(t, string(doc), response.Body.String())
}

// Every JSON content type uses the same duplicate-name policy before writes.
func TestAPIDuplicateFields(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
	}{
		{"exact", `{"note":"private-first","note":"private-second","amount":3,"highlighted":false}`, http.StatusBadRequest},
		{"alias last", `{"note":"private-first","Note":"private-second","amount":3,"highlighted":false}`, http.StatusUnprocessableEntity},
		{"alias first", `{"Note":"private-first","note":"private-second","amount":3,"highlighted":false}`, http.StatusUnprocessableEntity},
		{"two aliases", `{"Note":"private-first","NOTE":"private-second","amount":3,"highlighted":false}`, http.StatusUnprocessableEntity},
	}
	for _, contentType := range []string{"application/json", "application/vnd.inbok+json"} {
		for _, c := range cases {
			t.Run(contentType+"/"+c.name, func(t *testing.T) {
				probes := &fakeProbes{}
				var logs bytes.Buffer
				logger := slog.New(slog.NewTextHandler(&logs, nil))
				request := httptest.NewRequest(http.MethodPost, "/api/v0/probe/events", strings.NewReader(c.body))
				request.Header.Set("Content-Type", contentType)
				response := httptest.NewRecorder()

				New(logger, Services{Probe: probes}).Handler().ServeHTTP(response, request)

				require.Equal(t, c.status, response.Code, response.Body.String())
				assert.Empty(t, probes.added)
				assert.Equal(t, problemContentType, response.Header().Get("Content-Type"))
				body := decodeJSON(t, response.Body.Bytes())
				assert.InDelta(t, c.status, body["status"], 0)
				for _, secret := range []string{"private-first", "private-second"} {
					assert.NotContains(t, response.Body.String(), secret)
					assert.NotContains(t, logs.String(), secret)
				}
			})
		}
	}
}

// Gin's own 404 and 405 use the same Problem Details as Huma's errors.
func TestNoRoute(t *testing.T) {
	cases := []struct {
		method, path string
		status       int
		detail       string
		allow        string
	}{
		{http.MethodGet, "/api/v0/nope", http.StatusNotFound, "The requested route or resource was not found.", ""},
		{http.MethodGet, "/", http.StatusNotFound, "The requested route or resource was not found.", ""},
		{http.MethodGet, "/api/v0/schemas/HealthResponse.json", http.StatusNotFound, "The requested route or resource was not found.", ""},
		{http.MethodGet, "/schemas/HealthResponse.json", http.StatusNotFound, "The requested route or resource was not found.", ""},
		{http.MethodGet, "/api/v0/health/", http.StatusNotFound, "The requested route or resource was not found.", ""},
		{http.MethodDelete, "/api/v0/health", http.StatusMethodNotAllowed, "This route does not support that request method.", "GET"},
		{http.MethodHead, "/api/v0/health", http.StatusMethodNotAllowed, "This route does not support that request method.", "GET"},
	}

	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			var out bytes.Buffer
			response := httptest.NewRecorder()
			New(slog.New(slog.NewTextHandler(&out, nil)), Services{}).Handler().ServeHTTP(response, httptest.NewRequest(c.method, c.path, nil))

			require.Equal(t, c.status, response.Code)
			assert.Equal(t, "application/problem+json", response.Header().Get("Content-Type"))
			assert.Equal(t, c.allow, response.Header().Get("Allow"))
			assert.NotEmpty(t, response.Header().Get(requestIDHeader))

			body := decodeJSON(t, response.Body.Bytes())
			assert.InDelta(t, c.status, body["status"], 0)
			assert.NotNil(t, body["type"])
			assert.NotNil(t, body["title"])
			assert.Equal(t, c.detail, body["detail"])

			// The request log still records it.
			assert.Contains(t, out.String(), "msg=request")
			assert.Contains(t, out.String(), "status="+strconv.Itoa(c.status))
		})
	}
}
