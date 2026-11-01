package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// webApp is the built web app, served on paths outside /api.
type webApp interface {
	// Serve answers a GET or HEAD with a built file or page. Returns false,
	// having written nothing, if the build has none for the path.
	Serve(w http.ResponseWriter, request *http.Request) bool
}

// ServeWebApp serves app on every GET or HEAD outside /api. There, other
// methods get a 405 and paths app has nothing for get the API's 404. Call
// before serving requests.
func (a *API) ServeWebApp(app webApp) {
	a.router.NoRoute(func(ctx *gin.Context) {
		handleWebApp(ctx, app)
	})
}

// handleWebApp answers a request no API route matched with app, or with the
// API's 404 or 405.
func handleWebApp(ctx *gin.Context, app webApp) {
	if isAPIPath(ctx.Request.URL.Path) {
		handleNoRoute(ctx)
		return
	}
	if ctx.Request.Method != http.MethodGet && ctx.Request.Method != http.MethodHead {
		ctx.Header("Allow", "GET, HEAD")
		writeProblem(ctx, requestProblem(http.StatusMethodNotAllowed))
		return
	}
	// Gin presets 404 for a request no route matched; a response app
	// writes without a status gets the usual 200 instead.
	ctx.Status(http.StatusOK)
	if !app.Serve(ctx.Writer, ctx.Request) {
		handleNoRoute(ctx)
	}
}

// isAPIPath reports whether urlPath is /api or under /api/.
func isAPIPath(urlPath string) bool {
	// Gin routes //api/v0/health to no API route; it is still not a page.
	urlPath = "/" + strings.TrimLeft(urlPath, "/")
	// Later versions, e.g. /api/v1, belong to the API too.
	return urlPath == "/api" || strings.HasPrefix(urlPath, "/api/")
}
