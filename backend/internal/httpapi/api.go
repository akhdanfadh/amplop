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

const apiPrefix = "/api/v0"

// API serves inbok's HTTP operations and describes them as OpenAPI.
type API struct {
	router *gin.Engine
	api    huma.API
}

// New registers every operation. logger receives one line per request.
func New(logger *slog.Logger) *API {
	// Logs written with a request context gain its request_id.
	logger = slog.New(requestIDHandler{logger.Handler()})

	router := gin.New()
	// Request ID runs first so every log line has it. Recovery runs inside
	// the request log, so a panic is logged as 500.
	router.Use(withRequestID(), withRequestLog(logger), withRecovery(logger))
	router.HandleMethodNotAllowed = true
	router.NoRoute(handleNoRoute)
	router.NoMethod(handleNoMethod)
	group := router.Group(apiPrefix)

	config := huma.DefaultConfig("inbok", "0.0.0")
	// A relative server URL resolves against wherever the document is
	// served, e.g. https://mail.example.com/api/v0, with no host config.
	config.Servers = []*huma.Server{{URL: apiPrefix}}

	// No $schema field, Link header, or /schemas route: their URLs follow
	// the request's Host header and break behind a proxy. Clients read
	// schemas from the OpenAPI document instead.
	config.CreateHooks = nil
	config.SchemasPath = ""

	// Stop error responses leaking internals or user input.
	config.Transformers = []huma.Transformer{hideServerErrors(logger), hideInputValues}

	api := humagin.NewWithGroup(router, group, config)

	registerHealth(api)

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
	writeProblem(ctx, http.StatusNotFound, "route not found")
}

// handleNoMethod replaces Gin's plain-text 405 with Problem Details. Gin
// still sets the Allow header, e.g. "GET, HEAD".
func handleNoMethod(ctx *gin.Context) {
	writeProblem(ctx, http.StatusMethodNotAllowed, "method not allowed")
}
