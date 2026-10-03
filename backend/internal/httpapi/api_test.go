package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// discard silences request logs in tests that do not inspect them.
var discard = slog.New(slog.DiscardHandler)

func TestAPIDocs(t *testing.T) {
	router := New(discard).Handler()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v0/docs", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}

	if !strings.Contains(response.Body.String(), "/api/v0/openapi.yaml") {
		t.Fatal("documentation must load the versioned OpenAPI document")
	}
}

func TestOpenAPI(t *testing.T) {
	a := New(discard)
	doc, err := a.OpenAPI()
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.HasSuffix(doc, []byte("}\n")) {
		t.Fatal("document must end with a newline")
	}

	var exported any
	if err := json.Unmarshal(doc, &exported); err != nil {
		t.Fatal(err)
	}

	// The exported document must match the one the server serves.
	response := httptest.NewRecorder()
	a.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v0/openapi.json", nil))

	var served any
	if err := json.Unmarshal(response.Body.Bytes(), &served); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(exported, served) {
		t.Fatalf("exported document differs from served document:\n%s", doc)
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
		{http.MethodGet, "/api/v0/nope", http.StatusNotFound, "route not found", ""},
		{http.MethodGet, "/", http.StatusNotFound, "route not found", ""},
		{http.MethodGet, "/api/v0/schemas/HealthResponse.json", http.StatusNotFound, "route not found", ""},
		{http.MethodGet, "/schemas/HealthResponse.json", http.StatusNotFound, "route not found", ""},
		{http.MethodGet, "/api/v0/health/", http.StatusNotFound, "route not found", ""},
		{http.MethodDelete, "/api/v0/health", http.StatusMethodNotAllowed, "method not allowed", "GET"},
		{http.MethodHead, "/api/v0/health", http.StatusMethodNotAllowed, "method not allowed", "GET"},
	}

	for _, c := range cases {
		var out bytes.Buffer
		response := httptest.NewRecorder()
		New(slog.New(slog.NewTextHandler(&out, nil))).Handler().ServeHTTP(response, httptest.NewRequest(c.method, c.path, nil))

		name := c.method + " " + c.path
		if response.Code != c.status {
			t.Fatalf("%s: status = %d, want %d", name, response.Code, c.status)
		}
		if got := response.Header().Get("Content-Type"); got != "application/problem+json" {
			t.Fatalf("%s: Content-Type = %q", name, got)
		}
		if got := response.Header().Get("Allow"); got != c.allow {
			t.Fatalf("%s: Allow = %q, want %q", name, got, c.allow)
		}
		if response.Header().Get(requestIDHeader) == "" {
			t.Fatalf("%s: %s missing", name, requestIDHeader)
		}

		body := decodeJSON(t, response.Body.Bytes())
		if body["status"] != float64(c.status) || body["title"] != http.StatusText(c.status) || body["detail"] != c.detail {
			t.Fatalf("%s: body = %s", name, response.Body.String())
		}

		// The request log still records it.
		if !strings.Contains(out.String(), "msg=request") || !strings.Contains(out.String(), "status="+strconv.Itoa(c.status)) {
			t.Fatalf("%s: log = %s", name, out.String())
		}
	}
}
