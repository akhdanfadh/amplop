package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gin-gonic/gin"
)

// hideServerErrors logs a 5xx error's details and removes them from the
// response. Huma copies a handler's raw Go error into errors[], e.g.
// "pq: connection refused to 10.0.0.5:5432"; that must reach the log, not
// the client.
func hideServerErrors(logger *slog.Logger) huma.Transformer {
	return func(ctx huma.Context, _ string, v any) (any, error) {
		e, ok := v.(*huma.ErrorModel)
		if !ok || e.Status < http.StatusInternalServerError {
			return v, nil
		}

		for _, detail := range e.Errors {
			logger.LogAttrs(ctx.Context(), slog.LevelError, "unexpected error", slog.String("err", detail.Message))
		}

		e.Errors = nil
		return e, nil
	}
}

// hideInputValues clears each error detail's value, so a response never
// echoes user input, e.g. a rejected password. Location and message stay.
func hideInputValues(_ huma.Context, _ string, v any) (any, error) {
	e, ok := v.(*huma.ErrorModel)
	if !ok {
		return v, nil
	}

	for _, detail := range e.Errors {
		detail.Value = nil
	}

	return e, nil
}

// writeProblem answers a request Huma never handles, e.g. a Gin 404 or a
// recovered panic, with the same Problem Details body Huma writes.
func writeProblem(ctx *gin.Context, status int, detail string) {
	body, err := json.Marshal(huma.NewError(status, detail))
	if err != nil {
		ctx.AbortWithStatus(status)
		return
	}

	ctx.Data(status, "application/problem+json", body)
	ctx.Abort()
}
