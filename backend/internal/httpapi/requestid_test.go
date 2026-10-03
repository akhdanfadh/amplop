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
)

// Every log line of one request carries the ID returned to the client.
func TestRequestID(t *testing.T) {
	var out bytes.Buffer
	a := New(slog.New(slog.NewJSONHandler(&out, nil)), nil)
	a.router.GET("/panic", func(*gin.Context) { panic("boom") })

	response := httptest.NewRecorder()
	a.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/panic", nil))

	id := response.Header().Get(requestIDHeader)
	if _, err := uuid.Parse(id); err != nil {
		t.Fatalf("%s = %q, want a generated ID", requestIDHeader, id)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("log lines = %d, want panic and request:\n%s", len(lines), out.String())
	}

	for _, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		if entry["request_id"] != id {
			t.Fatalf("request_id = %v, want %q in %s", entry["request_id"], id, line)
		}
	}
}

// An incoming UUID from a reverse proxy is kept as sent, so its logs
// join ours. The second form is nginx's $request_id.
func TestRequestIDIncoming(t *testing.T) {
	for _, incoming := range []string{"0b9d6a56-3f4e-4c1a-9d2b-7e8f0a1b2c3d", "8f14e45fceea167a5a36dedd4bea2543"} {
		if got := requestIDFor(t, incoming); got != incoming {
			t.Fatalf("%s = %q, want the incoming %q", requestIDHeader, got, incoming)
		}
	}
}

// Non-UUID IDs are replaced so clients cannot inject log junk.
func TestRequestIDInvalid(t *testing.T) {
	for _, incoming := range []string{"proxy-7f3a", "has space", "new\nline", strings.Repeat("a", 65)} {
		got := requestIDFor(t, incoming)
		if _, err := uuid.Parse(got); got == incoming || err != nil {
			t.Fatalf("incoming %q: %s = %q, want a generated ID", incoming, requestIDHeader, got)
		}
	}
}

func requestIDFor(t *testing.T, incoming string) string {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, "/api/v0/health", nil)
	request.Header.Set(requestIDHeader, incoming)

	response := httptest.NewRecorder()
	New(discard, nil).Handler().ServeHTTP(response, request)

	return response.Header().Get(requestIDHeader)
}
