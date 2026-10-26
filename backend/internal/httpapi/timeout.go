package httpapi

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
)

// requestTimeout bounds each request's work, e.g. its database queries.
// It stays below the server's 30s WriteTimeout, so the handler still has
// time to write its error response.
const requestTimeout = 10 * time.Second

// withTimeout cancels a request's context after d. The server's
// WriteTimeout only stops writing; without this, a slow query would keep
// running after the client got cut off. A handler that hits it returns the
// context's error, which becomes a logged 500. A streaming handler can
// extend its own deadline with http.ResponseController.
func withTimeout(d time.Duration) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		timed, cancel := context.WithTimeout(ctx.Request.Context(), d)
		defer cancel()

		ctx.Request = ctx.Request.WithContext(timed)
		ctx.Next()
	}
}
