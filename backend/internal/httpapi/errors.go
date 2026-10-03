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
			// A nil error, e.g. Error409Conflict("x", nil), leaves a nil detail.
			if detail == nil {
				continue
			}
			logger.LogAttrs(ctx.Context(), slog.LevelError, "unexpected error", slog.String("err", detail.Message))
		}

		// Change a copy: e may be shared, e.g. a package-level var.
		out := *e
		out.Errors = nil
		return &out, nil
	}
}

// hideInputValues clears each error detail's value, so a response never
// echoes user input, e.g. a rejected password. Location and message stay.
func hideInputValues(_ huma.Context, _ string, v any) (any, error) {
	e, ok := v.(*huma.ErrorModel)
	if !ok {
		return v, nil
	}

	// Change copies: e may be shared, e.g. a package-level var.
	out := *e
	out.Errors = nil

	// A nil error, e.g. Error409Conflict("x", nil), leaves a nil detail that
	// would reach the client as null; drop it. No details omits errors.
	for _, detail := range e.Errors {
		if detail == nil {
			continue
		}
		hidden := *detail
		hidden.Value = nil
		out.Errors = append(out.Errors, &hidden)
	}

	return &out, nil
}

// problemContentType is the media type of Problem Details (RFC 9457).
const problemContentType = "application/problem+json"

// writeProblem answers a request Huma never handles, e.g. a Gin 404 or a
// recovered panic, with the same Problem Details body Huma writes.
func writeProblem(ctx *gin.Context, status int, detail string) {
	body, err := json.Marshal(huma.NewError(status, detail))
	if err != nil {
		ctx.AbortWithStatus(status)
		return
	}

	// Gin keeps a Content-Type the failed handler already set, e.g.
	// text/html; replace it so clients parse the body as Problem Details.
	ctx.Header("Content-Type", problemContentType)
	ctx.Data(status, problemContentType, body)
	ctx.Abort()
}
