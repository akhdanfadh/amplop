// Package httpapi registers inbok's HTTP operations.
package httpapi

import (
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
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
	EmailVerification emailVerificationService
	SystemEmail       systemEmailService
	TestMessages      testMessageService
	Disabler          disableService
	Backups           backupService
	CodeSignIn        codeSignInService
	Enrollment        enrollmentService
	TOTP              totpService
	Probe             probeService
	Account           accountService
	Session           sessionService
	Throttle          setupThrottle
}

// New registers operations; logger receives one line per request.
// Create all APIs and register all operations before serving requests.
func New(logger *slog.Logger, services Services) *API {
	// Tells Huma to construct errors using our problem model.
	configureErrors()

	// Logs written with a request context gain its request_id.
	logger = slog.New(requestIDHandler{logger.Handler()})

	router := gin.New()
	// Disable Gin's trust of all proxies. An empty list cannot fail parsing.
	router.SetTrustedProxies(nil)

	// Request ID runs first so every log line has it. Recovery runs inside
	// the request log, so a panic is logged as 500. Cross-origin rejection
	// is logged before the timeout wraps the handler's own work.
	router.Use(withRequestID(), withRequestLog(logger), withRecovery(logger), withCrossOrigin(), withTimeout(requestTimeout))
	router.HandleMethodNotAllowed = true
	// Gin would answer /health/ with a 301 to /health before any
	// middleware runs: no request ID, no log line, and a Location built
	// from the client's X-Forwarded-Prefix. A 404 is plain and logged.
	router.RedirectTrailingSlash = false
	router.NoRoute(handleNoRoute)
	router.NoMethod(handleNoMethod)
	group := router.Group(apiPrefix)

	config := huma.DefaultConfig(schemaTitle, schemaVersion)
	config.Info.Description = errorDocs

	// Reject duplicate names, including case aliases, while keeping other JSON
	// decoding rules compatible with Huma's default format.
	jsonFormat := huma.DefaultJSONFormat
	jsonFormat.Unmarshal = func(data []byte, value any) error {
		return jsonv2.Unmarshal(data, value, json.DefaultOptionsV1(), jsontext.AllowDuplicateNames(false))
	}
	config.Formats = map[string]huma.Format{"application/json": jsonFormat, "json": jsonFormat}

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
	api.UseMiddleware(withAccess(api, services.Session))

	registerHealth(api)
	registerProbe(api, services.Probe)
	registerAccount(api, services.Account, services.Throttle)
	registerSession(api, services.Session, services.CodeSignIn)
	registerTOTP(api, services.TOTP, services.Enrollment, services.Backups, services.Disabler)
	registerSystemEmail(api, services.SystemEmail, services.TestMessages)
	registerEmailVerification(api, services.EmailVerification)

	return &API{router: router, api: api}
}

// SetTrustedProxies sets which peers may supply forwarded client addresses.
// Call before serving requests; nil trusts no proxies.
func (a *API) SetTrustedProxies(cidrs []string) error {
	return a.router.SetTrustedProxies(cidrs)
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
