package httpapi

import (
	"uuid"

	"github.com/gin-gonic/gin"

	"inbok/internal/logging"
)

const requestIDHeader = "X-Request-ID"

// withRequestID tags each request with a UUID, returned in [requestIDHeader]
// and stored in the request context with [logging.WithRequestID]. An
// incoming ID that [uuid.Parse] accepts, e.g. nginx's 32-hex $request_id, is
// kept unchanged; anything else is replaced.
func withRequestID() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id := ctx.GetHeader(requestIDHeader)
		if _, err := uuid.Parse(id); err != nil {
			id = uuid.New().String()
		}

		ctx.Header(requestIDHeader, id)
		ctx.Request = ctx.Request.WithContext(logging.WithRequestID(ctx.Request.Context(), id))
		ctx.Next()
	}
}
