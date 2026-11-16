package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/logging"
	"inbok/internal/session"
)

// A handler waiting on slow work sees its context end after the timeout.
func TestTimeout(t *testing.T) {
	const d = 20 * time.Millisecond

	var err error
	router := gin.New()
	router.Use(withTimeout(d))
	router.GET("/slow", func(ctx *gin.Context) {
		select {
		case <-ctx.Request.Context().Done():
			err = ctx.Request.Context().Err()
		case <-time.After(time.Second):
		}
	})

	start := time.Now()
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/slow", nil))
	elapsed := time.Since(start)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.LessOrEqual(t, elapsed, 10*d)
}

// Every Huma operation gets the deadline.
func TestRequestTimeout(t *testing.T) {
	a := New(discard, Services{})

	var deadline time.Time
	var ok bool
	huma.Register(a.api, huma.Operation{
		OperationID: "getDeadline",
		Metadata:    map[string]any{accessKey: session.AccessPublic},
		Method:      http.MethodGet,
		Path:        "/deadline",
	}, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		deadline, ok = ctx.Deadline()
		return nil, nil
	})

	before := time.Now()
	a.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v0/deadline", nil))
	after := time.Now()

	// The middleware set it during the request: requestTimeout after a
	// moment between before and after.
	require.True(t, ok, "no deadline set")
	assert.WithinRange(t, deadline, before.Add(requestTimeout), after.Add(requestTimeout))
}

// shortTimeout is a request's time in tests that wait for it to run out.
const shortTimeout = 20 * time.Millisecond

// timeoutAPI returns an API whose requests get d, with one operation that
// waits for its context to end and returns what wait returns, and the
// buffer its log lines go to.
func timeoutAPI(t *testing.T, d time.Duration, wait func(ctx context.Context) error) (*API, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	a := newAPI(slog.New(logging.Handler(slog.NewJSONHandler(&out, nil))), Services{}, d)
	huma.Register(a.api, huma.Operation{
		OperationID: "getWait",
		Metadata:    map[string]any{accessKey: session.AccessPublic},
		Method:      http.MethodGet,
		Path:        "/wait",
	}, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		return nil, wait(ctx)
	})
	return a, &out
}

// getWait requests the wait operation with ctx.
func getWait(ctx context.Context, a *API) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	a.Handler().ServeHTTP(response, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v0/wait", nil))
	return response
}

// logLines returns the JSON lines in out.
func logLines(t *testing.T, out *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for line := range strings.Lines(out.String()) {
		lines = append(lines, decodeJSON(t, []byte(line)))
	}
	return lines
}

// A request still waiting when its time runs out answers 503 with a
// Retry-After of its time in whole seconds, rounded up, and logs a warning,
// not an unexpected error. The request's own log line stays at error level,
// like any 5xx, so a database that hangs still shows there.
func TestTimeoutServiceBusy(t *testing.T) {
	// Over a second, so the header shows the time, not the 1-second minimum.
	a, out := timeoutAPI(t, 1100*time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		// Wrapped, as a database driver or passwordhash returns it.
		return fmt.Errorf("wait for hash slot: %w", ctx.Err())
	})

	response := getWait(t.Context(), a)

	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	body := decodeJSON(t, response.Body.Bytes())
	assert.Equal(t, serviceBusyType, body["type"])
	assert.Equal(t, "Service busy", body["title"])
	assert.Equal(t, "The server is busy and could not finish the request in time.", body["detail"])
	assert.Equal(t, "2", response.Header().Get(retryAfterHeader))
	assert.NotContains(t, response.Body.String(), "hash slot", "body leaks the cause")

	var warned, requested bool
	for _, line := range logLines(t, out) {
		assert.NotEqual(t, "unexpected error", line["msg"])
		if line["msg"] == "request timed out" {
			warned = true
			assert.Equal(t, "WARN", line["level"])
			assert.Contains(t, line["err"], "wait for hash slot")
			assert.Equal(t, response.Header().Get(requestIDHeader), line["request_id"])
		}
		if line["msg"] == "request" {
			requested = true
			assert.Equal(t, "WARN", line["level"])
		}
	}
	assert.True(t, warned, "no warning logged")
	assert.True(t, requested, "no request line logged")
}

// A deadline error from inside a call, while the request still has time,
// stays an unexpected 500.
func TestTimeoutOtherDeadline(t *testing.T) {
	a, _ := timeoutAPI(t, time.Minute, func(context.Context) error {
		return fmt.Errorf("call an SMTP server: %w", context.DeadlineExceeded)
	})

	response := getWait(t.Context(), a)

	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Empty(t, response.Header().Get(retryAfterHeader))
}

// Another failure after the request's time ran out stays an unexpected 500.
func TestTimeoutOtherError(t *testing.T) {
	a, _ := timeoutAPI(t, shortTimeout, func(ctx context.Context) error {
		<-ctx.Done()
		return errors.New("disk full")
	})

	response := getWait(t.Context(), a)

	assert.Equal(t, http.StatusInternalServerError, response.Code)
}

// A request whose client left, failing with that cancellation, is logged
// as a closed request, 499 at info level, not as an unexpected error: its
// cancellation is neither a timeout nor a failure of inbok's.
func TestTimeoutClientGone(t *testing.T) {
	a, out := timeoutAPI(t, time.Minute, func(ctx context.Context) error {
		<-ctx.Done()
		// Wrapped, as a database driver returns it.
		return fmt.Errorf("query: %w", ctx.Err())
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	response := getWait(ctx, a)
	lines := logLines(t, out)

	assert.Equal(t, clientClosedStatus, response.Code)
	assert.Empty(t, response.Header().Get(retryAfterHeader))
	require.Len(t, lines, 2)
	for _, line := range lines {
		assert.Equal(t, "INFO", line["level"], "%v", line)
	}
	assert.Equal(t, "client closed request", lines[0]["msg"])
}

// A failure of inbok's own while the client leaves stays an unexpected
// 500, logged as an error.
func TestTimeoutClientGoneOtherError(t *testing.T) {
	a, out := timeoutAPI(t, time.Minute, func(ctx context.Context) error {
		<-ctx.Done()
		return errors.New("disk full")
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	response := getWait(ctx, a)
	lines := logLines(t, out)

	assert.Equal(t, http.StatusInternalServerError, response.Code)
	require.NotEmpty(t, lines)
	assert.Equal(t, "ERROR", lines[0]["level"])
	assert.Equal(t, "unexpected error", lines[0]["msg"])
}

// A failure of inbok's own joined with the cancellation, as some clients
// return both, stays an unexpected 500 when the client leaves.
func TestTimeoutClientGoneJoined(t *testing.T) {
	a, out := timeoutAPI(t, time.Minute, func(ctx context.Context) error {
		<-ctx.Done()
		return errors.Join(errors.New("database connection failed"), fmt.Errorf("wait: %w", ctx.Err()))
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	response := getWait(ctx, a)
	lines := logLines(t, out)

	assert.Equal(t, http.StatusInternalServerError, response.Code)
	require.NotEmpty(t, lines)
	assert.Equal(t, "ERROR", lines[0]["level"])
}

// Only the cancellation itself, wrapped or joined with itself, is one.
func TestOnlyCanceled(t *testing.T) {
	cases := map[string]struct {
		err  error
		want bool
	}{
		"bare":            {context.Canceled, true},
		"wrapped":         {fmt.Errorf("query: %w", context.Canceled), true},
		"joined, alone":   {errors.Join(context.Canceled, fmt.Errorf("a: %w", context.Canceled)), true},
		"joined, failure": {errors.Join(context.Canceled, errors.New("disk full")), false},
		"deadline":        {context.DeadlineExceeded, false},
		"other":           {errors.New("disk full"), false},
		"nil":             {nil, false},
	}
	for name, c := range cases {
		assert.Equal(t, c.want, onlyCanceled(c.err), name)
	}
}

// A cancellation from inside a call, e.g. a context the handler made,
// while the client is still there, stays an unexpected 500.
func TestTimeoutOwnCancel(t *testing.T) {
	a, _ := timeoutAPI(t, time.Minute, func(ctx context.Context) error {
		inner, cancel := context.WithCancel(ctx)
		cancel()
		return inner.Err()
	})

	response := getWait(t.Context(), a)

	assert.Equal(t, http.StatusInternalServerError, response.Code)
}

// A problem the handler mapped keeps its status after the time ran out.
func TestTimeoutMappedProblem(t *testing.T) {
	a, _ := timeoutAPI(t, shortTimeout, func(ctx context.Context) error {
		<-ctx.Done()
		return newProblem(http.StatusConflict, problemPrefix+"counter-changed", "Counter changed", "The counter changed.")
	})

	response := getWait(t.Context(), a)

	assert.Equal(t, http.StatusConflict, response.Code)
}
