package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"uuid"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/logging"
)

// Every log line of one request carries the ID returned to the client.
func TestRequestID(t *testing.T) {
	var out bytes.Buffer
	a := New(slog.New(logging.Handler(slog.NewJSONHandler(&out, nil))), Services{})
	a.router.GET("/panic", func(*gin.Context) { panic("boom") })

	response := httptest.NewRecorder()
	a.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/panic", nil))

	id := response.Header().Get(requestIDHeader)
	_, err := uuid.Parse(id)
	require.NoError(t, err, "%s = %q, want a generated ID", requestIDHeader, id)

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 2, "want panic and request")

	for _, line := range lines {
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		assert.Equal(t, id, entry["request_id"], line)
	}
}

// An incoming UUID from a reverse proxy is kept as sent, so its logs
// join ours. The second form is nginx's $request_id.
func TestRequestIDIncoming(t *testing.T) {
	for _, incoming := range []string{"0b9d6a56-3f4e-4c1a-9d2b-7e8f0a1b2c3d", "8f14e45fceea167a5a36dedd4bea2543"} {
		assert.Equal(t, incoming, requestIDFor(t, incoming))
	}
}

// Non-UUID IDs are replaced so clients cannot inject log junk.
func TestRequestIDInvalid(t *testing.T) {
	for _, incoming := range []string{"proxy-7f3a", "has space", "new\nline", strings.Repeat("a", 65)} {
		got := requestIDFor(t, incoming)
		assert.NotEqual(t, incoming, got)

		_, err := uuid.Parse(got)
		require.NoError(t, err, "incoming %q: %s = %q, want a generated ID", incoming, requestIDHeader, got)
	}
}

func requestIDFor(t *testing.T, incoming string) string {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, "/api/v0/health", nil)
	request.Header.Set(requestIDHeader, incoming)

	response := httptest.NewRecorder()
	New(discard, Services{}).Handler().ServeHTTP(response, request)

	return response.Header().Get(requestIDHeader)
}
