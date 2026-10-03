package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRecovery(t *testing.T) {
	var out bytes.Buffer
	a := New(slog.New(slog.NewJSONHandler(&out, nil)))
	// The handler's own Content-Type must not label the error body.
	a.router.GET("/panic", func(ctx *gin.Context) {
		ctx.Header("Content-Type", "text/html")
		panic("secret detail")
	})

	response := httptest.NewRecorder()
	a.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/panic", nil))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}

	if got := response.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("Content-Type = %q", got)
	}

	// The client sees Huma's error shape, never the panic value.
	var problem map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem["status"] != float64(http.StatusInternalServerError) || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("body = %s", response.Body.String())
	}

	// Expect the panic entry, then the request entry with status 500.
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("log lines = %d, want 2:\n%s", len(lines), out.String())
	}

	var panicked, request map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &panicked); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &request); err != nil {
		t.Fatal(err)
	}

	stack, _ := panicked["stack"].(string)
	if panicked["msg"] != "panic" || panicked["level"] != "ERROR" || panicked["value"] != "secret detail" || stack == "" {
		t.Fatalf("panic entry = %v", panicked)
	}

	if request["msg"] != "request" || request["status"] != float64(http.StatusInternalServerError) {
		t.Fatalf("request entry = %v", request)
	}
}

// net/http uses http.ErrAbortHandler to abort a response on purpose.
func TestRecoveryAbort(t *testing.T) {
	a := New(discard)
	a.router.GET("/abort", func(*gin.Context) { panic(http.ErrAbortHandler) })

	defer func() {
		if v := recover(); !errors.Is(asError(v), http.ErrAbortHandler) {
			t.Fatalf("recovered %v, want http.ErrAbortHandler", v)
		}
	}()

	a.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/abort", nil))
	t.Fatal("ErrAbortHandler must reach net/http")
}

func asError(v any) error {
	err, _ := v.(error)
	return err
}
