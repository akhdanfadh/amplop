package httpapi

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/gin-gonic/gin"
)

// requestTimeout bounds each request's work, e.g. its database queries.
// It stays below the server's 30s WriteTimeout, so the handler still has
// time to write its error response.
const requestTimeout = 10 * time.Second

// serviceBusyType is the problem of a request that timed out.
const serviceBusyType = problemPrefix + "service-busy"

// withTimeout cancels a request's context after d. The server's
// WriteTimeout only stops writing; without this, a slow query would keep
// running after the client got cut off. A handler that hits it returns the
// context's error, which becomes [serviceBusyProblem]. A streaming handler
// can extend its own deadline with http.ResponseController.
func withTimeout(d time.Duration) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		timed, cancel := context.WithTimeout(ctx.Request.Context(), d)
		defer cancel()

		ctx.Request = ctx.Request.WithContext(timed)
		ctx.Next()
	}
}

// serviceBusyProblem returns the 503 for a request that timed out.
func serviceBusyProblem() *problem {
	return newProblem(http.StatusServiceUnavailable, serviceBusyType, "Service busy", "The server is busy and could not finish the request in time.")
}

// requestTimedOut reports whether the deadline of the request with ctx caused
// errs: ctx has passed its deadline, and one of errs is
// [context.DeadlineExceeded]. A deadline inside a call, while the request
// still has time, does not count.
func requestTimedOut(ctx context.Context, errs []error) bool {
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return false
	}
	return slices.ContainsFunc(errs, func(err error) bool {
		return errors.Is(err, context.DeadlineExceeded)
	})
}
