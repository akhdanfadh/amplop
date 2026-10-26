package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestLog(t *testing.T) {
	var out bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&out, nil))

	router := New(logger, Services{}).Handler()
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v0/health?token=secret", nil))

	var entry map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &entry), out.String())

	want := map[string]any{
		"level":  "INFO",
		"msg":    "request",
		"method": http.MethodGet,
		"path":   "/api/v0/health", // query strings may carry secrets
		"status": float64(http.StatusOK),
	}
	assert.Subset(t, entry, want)
	assert.Contains(t, entry, "duration")
}
