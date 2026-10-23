// Package httpapi registers inbok's HTTP operations.
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/gin-gonic/gin"
)

const schemaTitle = "inbok"
const schemaVersion = "0.0.0"
const apiPrefix = "/api/v0"

// API serves inbok's HTTP operations and describes them as OpenAPI.
type API struct {
	router *gin.Engine
	api    huma.API
}

// Services holds what each feature's operations call. Fields may be nil
// when no request is served, e.g. to export OpenAPI: registering an
// operation calls none of them.
type Services struct {
	Probe probeService
}

// New registers operations; logger receives one line per request.
// Create all APIs and register all operations before serving requests.
func New(logger *slog.Logger, services Services) *API {
	// Tells Huma to construct errors using our problem model.
	configureErrors()

	// Logs written with a request context gain its request_id.
	logger = slog.New(requestIDHandler{logger.Handler()})

	router := gin.New()
	// Request ID runs first so every log line has it. Recovery runs inside
	// the request log, so a panic is logged as 500. The timeout wraps only
	// the handler's own work.
	router.Use(withRequestID(), withRequestLog(logger), withRecovery(logger), withTimeout(requestTimeout))
	router.HandleMethodNotAllowed = true
	// Gin would answer /health/ with a 301 to /health before any
	// middleware runs: no request ID, no log line, and a Location built
	// from the client's X-Forwarded-Prefix. A 404 is plain and logged.
	router.RedirectTrailingSlash = false
	router.NoRoute(handleNoRoute)
	router.NoMethod(handleNoMethod)
	group := router.Group(apiPrefix)

	config := huma.DefaultConfig(schemaTitle, schemaVersion)
	// A relative server URL resolves against wherever the document is
	// served, e.g. https://mail.example.com/api/v0, with no host config.
	config.Servers = []*huma.Server{{URL: apiPrefix}}

	// No $schema field, Link header, or /schemas route: their URLs follow
	// the request's Host header and break behind a proxy. Clients read
	// schemas from the OpenAPI document instead.
	config.CreateHooks = nil
	config.SchemasPath = ""

	// The callback runs after api is assigned; normalization needs its schemas
	// to format safe input messages before Huma converts the response to JSON.
	var api huma.API
	config.Transformers = []huma.Transformer{func(ctx huma.Context, _ string, value any) (any, error) {
		return normalizeError(logger, api, ctx, value), nil
	}}

	api = humagin.NewWithGroup(router, group, config)

	registerHealth(api)
	registerProbe(api, services.Probe)

	return &API{router: router, api: api}
}

// Handler returns the handler for every registered route.
func (a *API) Handler() http.Handler {
	return a.router
}

// OpenAPI returns the document indented, so committed copies diff well.
func (a *API) OpenAPI() ([]byte, error) {
	doc, err := json.MarshalIndent(a.api.OpenAPI(), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(doc, '\n'), nil
}

// handleNoRoute replaces Gin's plain-text 404 with Problem Details.
func handleNoRoute(ctx *gin.Context) {
	writeProblem(ctx, requestProblem(http.StatusNotFound))
}

// handleNoMethod replaces Gin's plain-text 405 with Problem Details. Gin
// still sets the Allow header, e.g. "GET".
func handleNoMethod(ctx *gin.Context) {
	writeProblem(ctx, requestProblem(http.StatusMethodNotAllowed))
}
