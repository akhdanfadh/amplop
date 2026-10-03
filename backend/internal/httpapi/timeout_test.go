package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gin-gonic/gin"
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

	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 10*d {
		t.Fatalf("err = %v after %v, want a deadline after %v", err, time.Since(start), d)
	}
}

// Every Huma operation gets the deadline.
func TestRequestTimeout(t *testing.T) {
	a := New(discard, Services{})

	var deadline time.Time
	var ok bool
	huma.Register(a.api, huma.Operation{
		OperationID: "getDeadline",
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
	if !ok || deadline.Before(before.Add(requestTimeout)) || deadline.After(after.Add(requestTimeout)) {
		t.Fatalf("deadline = %v (set: %v), want %v after the request started", deadline, ok, requestTimeout)
	}
}
