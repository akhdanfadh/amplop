package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecovery(t *testing.T) {
	var out bytes.Buffer
	a := New(slog.New(slog.NewJSONHandler(&out, nil)), Services{})
	// The handler's own Content-Type must not label the error body.
	a.router.GET("/panic", func(ctx *gin.Context) {
		ctx.Header("Content-Type", "text/html")
		panic("secret detail")
	})

	response := httptest.NewRecorder()
	a.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/panic", nil))

	require.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Equal(t, "application/problem+json", response.Header().Get("Content-Type"))

	// The client sees Huma's error shape, never the panic value.
	var problem map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
	assert.InDelta(t, http.StatusInternalServerError, problem["status"], 0)
	assert.Equal(t, "/api/v0/problems/internal-error", problem["type"])
	assert.Equal(t, "Something went wrong on our side.", problem["detail"])
	assert.NotContains(t, response.Body.String(), "secret")

	// Expect the panic entry, then the request entry with status 500.
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 2)

	var panicked, request map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &panicked))
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &request))

	assert.Equal(t, "panic", panicked["msg"])
	assert.Equal(t, "ERROR", panicked["level"])
	assert.Equal(t, "secret detail", panicked["value"])
	assert.NotEmpty(t, panicked["stack"])

	assert.Equal(t, "request", request["msg"])
	assert.InDelta(t, http.StatusInternalServerError, request["status"], 0)
}

// net/http uses http.ErrAbortHandler to abort a response on purpose.
func TestRecoveryAbort(t *testing.T) {
	a := New(discard, Services{})
	a.router.GET("/abort", func(*gin.Context) { panic(http.ErrAbortHandler) })

	defer func() {
		v := recover()
		require.ErrorIs(t, asError(v), http.ErrAbortHandler, "recovered %v", v)
	}()

	a.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/abort", nil))
	require.FailNow(t, "ErrAbortHandler must reach net/http")
}

func asError(v any) error {
	err, _ := v.(error)
	return err
}
