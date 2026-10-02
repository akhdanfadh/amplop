package httpapi

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
)

// withRecovery turns a handler panic into a logged 500 Problem Details
// response, so one bad request cannot drop the connection unanswered.
func withRecovery(logger *slog.Logger) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}

			// net/http aborts a response on purpose with this value.
			if v == http.ErrAbortHandler {
				panic(v)
			}

			logger.LogAttrs(ctx.Request.Context(), slog.LevelError, "panic",
				slog.Any("value", v),
				slog.String("stack", string(debug.Stack())),
			)

			// Headers already sent: the status cannot change anymore.
			if ctx.Writer.Written() {
				ctx.Abort()
				return
			}

			// Same body as Huma's own unexpected errors; the panic value
			// stays in the log, never in the response.
			writeProblem(ctx, http.StatusInternalServerError, "unexpected error occurred")
		}()

		ctx.Next()
	}
}
