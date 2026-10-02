package httpapi

import (
	"context"
	"log/slog"
	"uuid"

	"github.com/gin-gonic/gin"
)

const requestIDHeader = "X-Request-ID"

type requestIDKey struct{}

// withRequestID tags each request with a UUID, returned in [requestIDHeader]
// and stored in the request context for logging. An incoming ID that
// [uuid.Parse] accepts, e.g. nginx's 32-hex $request_id, is kept unchanged;
// anything else is replaced.
func withRequestID() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id := ctx.GetHeader(requestIDHeader)
		if _, err := uuid.Parse(id); err != nil {
			id = uuid.New().String()
		}

		ctx.Header(requestIDHeader, id)
		ctx.Request = ctx.Request.WithContext(context.WithValue(ctx.Request.Context(), requestIDKey{}, id))
		ctx.Next()
	}
}

// requestIDHandler adds request_id to every record logged with a request
// context, e.g. logger.LogAttrs(ctx.Request.Context(), ...).
type requestIDHandler struct {
	slog.Handler
}

func (h requestIDHandler) Handle(ctx context.Context, record slog.Record) error {
	if id, ok := ctx.Value(requestIDKey{}).(string); ok {
		record.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, record)
}

// WithAttrs and WithGroup keep the wrapper; the embedded versions would
// return the inner handler and drop request_id.
func (h requestIDHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return requestIDHandler{h.Handler.WithAttrs(attrs)}
}

func (h requestIDHandler) WithGroup(name string) slog.Handler {
	return requestIDHandler{h.Handler.WithGroup(name)}
}
