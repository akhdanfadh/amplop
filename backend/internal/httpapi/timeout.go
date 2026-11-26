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
// can extend its own deadline with [http.ResponseController].
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

// clientClosedStatus is the status a request whose client left answers
// with: nginx's 499 Client Closed Request, which no HTTP standard defines.
// A client rarely receives it, e.g. one that closed only its sending side.
const clientClosedStatus = 499

// clientClosedType is the problem of a request whose client left.
const clientClosedType = problemPrefix + "client-closed-request"

// clientClosedProblem returns the problem of a request whose client left,
// which the request log shows and a client rarely receives.
func clientClosedProblem() *problem {
	return newProblem(clientClosedStatus, clientClosedType, "Client closed request", "The client closed the request before it was answered.")
}

// clientClosed reports whether ctx, a request's, was cancelled, which in
// net/http means its client left, and errs, the handler's, are that
// cancellation and nothing else, so a failure of amplop's own while the
// client leaves still counts as one, even joined with the cancellation.
func clientClosed(ctx context.Context, errs []error) bool {
	if !errors.Is(ctx.Err(), context.Canceled) || len(errs) == 0 {
		return false
	}
	return !slices.ContainsFunc(errs, func(err error) bool { return !onlyCanceled(err) })
}

// onlyCanceled reports whether err is [context.Canceled], wrapped or not,
// in every branch: errors.Is would also find it beside another failure in
// a joined error.
func onlyCanceled(err error) bool {
	switch e := err.(type) {
	case nil:
		return false
	case interface{ Unwrap() []error }:
		branches := e.Unwrap()
		return len(branches) > 0 && !slices.ContainsFunc(branches, func(b error) bool { return !onlyCanceled(b) })
	case interface{ Unwrap() error }:
		return onlyCanceled(e.Unwrap())
	}
	return errors.Is(err, context.Canceled)
}
