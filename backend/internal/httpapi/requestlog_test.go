package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
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

// A request's line is a warning for a 503, which is temporary, and an error
// for any other 5xx.
func TestRequestLogLevel(t *testing.T) {
	cases := map[int]string{
		http.StatusOK:                  "INFO",
		http.StatusNotFound:            "INFO",
		http.StatusInternalServerError: "ERROR",
		http.StatusBadGateway:          "ERROR",
		http.StatusServiceUnavailable:  "WARN",
	}
	for status, level := range cases {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var out bytes.Buffer
			router := gin.New()
			router.Use(withRequestLog(slog.New(slog.NewJSONHandler(&out, nil))))
			router.GET("/status", func(ctx *gin.Context) { ctx.Status(status) })

			router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/status", nil))

			var entry map[string]any
			require.NoError(t, json.Unmarshal(out.Bytes(), &entry), out.String())
			assert.Equal(t, level, entry["level"])
		})
	}
}
