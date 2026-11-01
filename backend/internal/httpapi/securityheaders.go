package httpapi

import "github.com/gin-gonic/gin"

// withSecurityHeaders sets the browser protections every response shares,
// the API's and the web app's.
func withSecurityHeaders() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		header := ctx.Writer.Header()
		// Browsers use the declared Content-Type, never a guess: a JSON or
		// text response cannot run as a script or stylesheet.
		header.Set("X-Content-Type-Options", "nosniff")
		// Leaving inbok sends no URL, which can name an account's page.
		header.Set("Referrer-Policy", "no-referrer")
		// No site may frame a page, e.g. to trick a click. The web app's
		// CSP says the same with frame-ancestors, as does Huma's on its docs
		// page; this older header also covers browsers without CSP Level 2.
		header.Set("X-Frame-Options", "DENY")
		ctx.Next()
	}
}
