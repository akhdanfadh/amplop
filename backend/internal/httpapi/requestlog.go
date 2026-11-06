package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// withRequestLog logs one line per request after it completes: a warning
// for a 503, an error for any other 5xx, and info otherwise. The path
// excludes the query string, which may carry secrets.
func withRequestLog(logger *slog.Logger) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		start := time.Now()
		ctx.Next()

		status := ctx.Writer.Status()
		level := slog.LevelInfo
		switch {
		// A 503 says the server is busy or down for a while, not broken.
		case status == http.StatusServiceUnavailable:
			level = slog.LevelWarn
		case status >= http.StatusInternalServerError:
			level = slog.LevelError
		}

		logger.LogAttrs(ctx.Request.Context(), level, "request",
			slog.String("method", ctx.Request.Method),
			slog.String("path", ctx.Request.URL.Path),
			slog.Int("status", status),
			slog.Duration("duration", time.Since(start)),
		)
	}
}
