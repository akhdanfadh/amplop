package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestLog(t *testing.T) {
	var out bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&out, nil))

	router := New(logger).Handler()
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v0/health?token=secret", nil))

	var entry map[string]any
	if err := json.Unmarshal(out.Bytes(), &entry); err != nil {
		t.Fatalf("log = %q: %v", out.String(), err)
	}

	want := map[string]any{
		"level":  "INFO",
		"msg":    "request",
		"method": http.MethodGet,
		"path":   "/api/v0/health", // query strings may carry secrets
		"status": float64(http.StatusOK),
	}
	for key, value := range want {
		if entry[key] != value {
			t.Fatalf("%s = %v, want %v in %v", key, entry[key], value, entry)
		}
	}

	if _, ok := entry["duration"]; !ok {
		t.Fatalf("duration missing in %v", entry)
	}
}
