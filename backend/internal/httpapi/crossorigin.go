package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// withCrossOrigin rejects unsafe cross-origin browser requests before handlers.
// Go validates Fetch Metadata or Origin; writes must include at least one.
func withCrossOrigin() gin.HandlerFunc {
	protection := http.NewCrossOriginProtection()
	return func(ctx *gin.Context) {
		ctx.Writer.Header().Add("Vary", "Sec-Fetch-Site, Origin")
		switch ctx.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			ctx.Next()
			return
		}

		missingSource := ctx.GetHeader("Sec-Fetch-Site") == "" && ctx.GetHeader("Origin") == ""
		if missingSource || protection.Check(ctx.Request) != nil {
			writeProblem(ctx, crossOriginProblem())
			return
		}

		ctx.Next()
	}
}

// crossOriginProblem returns the 403 for a write whose source is missing or
// another site.
func crossOriginProblem() *problem {
	return newProblem(http.StatusForbidden, problemPrefix+"cross-origin-request",
		"Cross-origin request blocked", "Send this request from the inbok site.")
}
