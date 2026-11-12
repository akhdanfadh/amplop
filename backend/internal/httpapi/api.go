// Package httpapi registers inbok's HTTP operations.
package httpapi

import (
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/gin-gonic/gin"
)

const schemaTitle = "inbok"
const schemaVersion = "0.0.0"
const apiPrefix = "/api/v0"

// API serves inbok's HTTP operations and describes them as OpenAPI.
type API struct {
	router  *gin.Engine
	api     huma.API
	limiter *rateLimiter // kept so tests can move its clock
}

// Services holds what each feature's operations call. Fields may be nil
// when no request is served, e.g. to export OpenAPI: registering an
// operation calls none of them.
type Services struct {
	EmailVerification       emailVerificationService
	Members                 membersService
	SystemEmail             systemEmailService
	TestMessages            testMessageService
	Registration            registrationService
	RegistrationRequests    registrationRequestService
	RegistrationCompletion  registrationCompletionService
	PasswordResetRequests   passwordResetRequestService
	PasswordResetCompletion passwordResetCompletionService
	Disabler                disableService
	Backups                 backupService
	CodeSignIn              codeSignInService
	Enrollment              enrollmentService
	TOTP                    totpService
	Probe                   probeService
	Account                 accountService
	Session                 sessionService
	Throttle                setupThrottle
	ConnectedAccounts       connectedAccountService
	// Connections is nil when no Google client is configured, and Gmail is
	// then not offered.
	Connections      connectionService
	OutgoingMessages outgoingMessageService
	Syncs            syncService
	MessageContent   messageContentService
	Conversations    conversationService
	// ConversationActions is nil when no Google client is configured, and
	// actions then refuse with a 409.
	ConversationActions conversationActionService
}

// New registers operations; logger receives one line per request. Build it
// with [logging.New] so lines logged with a request's context carry its
// request_id.
// Create all APIs and register all operations before serving requests.
func New(logger *slog.Logger, services Services) *API {
	return newAPI(logger, services, requestTimeout)
}

// newAPI is [New] with timeout as each request's time.
func newAPI(logger *slog.Logger, services Services, timeout time.Duration) *API {
	// Tells Huma to construct errors using our problem model.
	configureErrors()

	router := gin.New()
	// Disable Gin's trust of all proxies. An empty list cannot fail parsing.
	router.SetTrustedProxies(nil)

	// Request ID runs first so every log line has it. Security headers
	// come next, so even a rejected or failed request carries them.
	// Recovery runs inside the request log, so a panic is logged as 500.
	// Cross-origin rejection is logged before the timeout wraps the
	// handler's own work.
	router.Use(withRequestID(), withSecurityHeaders(), withRequestLog(logger), withRecovery(logger), withCrossOrigin(), withTimeout(timeout))
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
	config.OpenAPI.Tags = apiTags()

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
		return normalizeError(logger, api, ctx, value, timeout), nil
	}}

	api = humagin.NewWithGroup(router, group, config)
	// Floods are refused before the access check and body parsing, and a
	// request of a kind the operation does not answer, e.g. an image,
	// before the access check reads its session.
	limiter := newRateLimiter(time.Now)
	api.UseMiddleware(withRateLimit(limiter), withFetchDest(), withAccess(api, services.Session))

	registerHealth(api)
	registerProbe(api, services.Probe)
	registerAccount(api, services.Account, services.Throttle)
	registerSession(api, services.Session, services.CodeSignIn)
	registerTOTP(api, services.TOTP, services.Enrollment, services.Backups, services.Disabler)
	registerSystemEmail(api, services.SystemEmail, services.TestMessages)
	registerEmailVerification(api, services.EmailVerification)
	registerMembers(api, services.Members)
	registerRegistration(api, services.Registration, services.RegistrationRequests, services.RegistrationCompletion)
	registerPasswordReset(api, services.PasswordResetRequests, services.PasswordResetCompletion)
	registerConnectedAccounts(api, logger, services.ConnectedAccounts, services.Connections, services.Syncs, services.Session)
	registerOutgoingMessages(api, services.OutgoingMessages)
	registerMessageContent(api, services.MessageContent)
	registerConversations(api, services.Conversations, services.ConversationActions)

	documentRateLimits(api)
	registerProblemTypes(api)

	return &API{router: router, api: api, limiter: limiter}
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

// operations returns every operation in doc.
func operations(doc *huma.OpenAPI) []*huma.Operation {
	var ops []*huma.Operation
	for _, item := range doc.Paths {
		for _, op := range []*huma.Operation{item.Get, item.Put, item.Post, item.Delete, item.Options, item.Head, item.Patch, item.Trace} {
			if op != nil {
				ops = append(ops, op)
			}
		}
	}
	return ops
}
