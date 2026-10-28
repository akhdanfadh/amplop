package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
