package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"inbok/internal/account"
	"inbok/internal/emailaddress"
	"inbok/internal/session"
)

const setupPath = "/setup"

// accountService is the account behavior called by these HTTP operations.
type accountService interface {
	SetupStatus(ctx context.Context) (account.SetupStatus, error)
	Setup(ctx context.Context, input account.SetupInput) (account.User, error)
}

// firstAdminSignIn signs in the administrator setup just created.
type firstAdminSignIn interface {
	SignInFirstAdmin(ctx context.Context, userID int64, device session.Device) (session.SignedIn, error)
}

// accountHandler holds the dependencies shared by setup HTTP handlers.
type accountHandler struct {
	service  accountService
	throttle setupThrottle
	signIn   firstAdminSignIn
}

func registerAccount(api huma.API, service accountService, throttle setupThrottle, signIn firstAdminSignIn) {
	handler := accountHandler{service: service, throttle: throttle, signIn: signIn}
	registerSetupStatus(api, handler)
	registerFirstAdmin(api, handler)
}

// ========================
// Setup status
// ========================

type setupStatus struct {
	Status account.SetupStatus `json:"status" enum:"unconfigured,open,done"`
}

// registerSetupStatus adds the public setup-state operation.
func registerSetupStatus(api huma.API, handler accountHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "getSetupStatus",
		Metadata:    map[string]any{accessKey: session.AccessPublic},
		Method:      http.MethodGet,
		Path:        setupPath,
		Summary:     "Get Setup Status",
		Tags:        []string{tagSetup},
		Description: "Whether the first administrator can be created: done once an administrator exists, and done stays done; otherwise unconfigured without a setup token, or open with one.",
		Responses:   setupStatusResponses(api),
	}, handler.handleGetSetupStatus)
}

// handleGetSetupStatus reports the first-admin setup state.
func (h accountHandler) handleGetSetupStatus(ctx context.Context, _ *struct{}) (*output[setupStatus], error) {
	status, err := h.service.SetupStatus(ctx)
	if err != nil {
		return nil, err
	}
	return respondData(setupStatus{Status: status}), nil
}

func setupStatusResponses(api huma.API) map[string]*huma.Response {
	return problemResponses(api)
}

// ========================
// First administrator
// ========================

const (
	setupDoneType         = problemPrefix + "setup-done"
	setupUnconfiguredType = problemPrefix + "setup-unconfigured"
	setupTokenInvalidType = problemPrefix + "setup-token-invalid"
)

// setupThrottle reserves setup failures against the shared address limit.
type setupThrottle interface {
	BeginSetup(ctx context.Context, addr netip.Addr) (int64, error)
	Succeed(ctx context.Context, id int64) error
}

// newSetup is the request body. Huma checks its fields before calling Setup.
type newSetup struct {
	Token    string `json:"token" writeOnly:"true"`
	Email    string `json:"email" format:"idn-email" minLength:"3" maxLength:"254" doc:"Letters A to Z are saved in lowercase."`
	Password string `json:"password" writeOnly:"true" minLength:"15" maxLength:"128"`
}

// setupInput combines setup fields with the requesting browser's details.
type setupInput struct {
	Body   newSetup
	device session.Device
}

// Resolve reads the device details: the User-Agent and the client address
// Gin's proxy rules select, used for throttling and kept by the new session.
// Huma calls it automatically after parsing the request, before the handler.
func (in *setupInput) Resolve(ctx huma.Context) []error {
	return resolveDevice(ctx, &in.device)
}

// firstAdminOutput carries the new administrator and its session cookie.
type firstAdminOutput struct {
	SetCookie []http.Cookie `header:"Set-Cookie"`
	Body      response[firstAdmin]
}

type firstAdmin struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
}

// registerFirstAdmin adds the first-administrator operation.
func registerFirstAdmin(api huma.API, handler accountHandler) {
	huma.Register(api, huma.Operation{
		OperationID:   "createFirstAdmin",
		Metadata:      map[string]any{accessKey: session.AccessPublic},
		Method:        http.MethodPost,
		Path:          setupPath,
		Summary:       "Create First Administrator",
		Tags:          []string{tagSetup},
		Description:   "Creates the first administrator with the setup token and signs them in with a 15-minute session that can only set up two-factor authentication. Works once, and only while a setup token is configured.",
		DefaultStatus: http.StatusCreated,
		Responses:     firstAdminResponses(api),
	}, handler.handleCreateFirstAdmin)
}

// handleCreateFirstAdmin creates the first administrator and signs them in.
func (h accountHandler) handleCreateFirstAdmin(ctx context.Context, in *setupInput) (*firstAdminOutput, error) {
	// 1. Reserve a throttle attempt before claiming setup.
	id, err := h.throttle.BeginSetup(ctx, in.device.Addr)
	if err != nil {
		return nil, throttleProblem(err)
	}

	// 2. Claim setup. A failure keeps the attempt, so it counts.
	user, err := h.service.Setup(ctx, account.SetupInput{
		Token:    in.Body.Token,
		Email:    in.Body.Email,
		Password: in.Body.Password,
	})
	if err != nil {
		return nil, firstAdminProblem(err)
	}
	if err := h.throttle.Succeed(ctx, id); err != nil {
		return nil, err
	}

	// 3. Sign the administrator in, only once they exist. If this fails,
	// they sign in as usual.
	signedIn, err := h.signIn.SignInFirstAdmin(ctx, user.ID, in.device)
	if err != nil {
		return nil, err
	}
	maxAge := int(time.Until(signedIn.ExpiresAt) / time.Second)
	return &firstAdminOutput{
		SetCookie: []http.Cookie{sessionCookie(signedIn.Secret, maxAge), challengeCookie("", -1)},
		Body:      respondData(firstAdmin{ID: user.ID, Email: user.Email}).Body,
	}, nil
}

// firstAdminResponses documents the operation's expected failures.
func firstAdminResponses(api huma.API) map[string]*huma.Response {
	responses := problemResponses(api, http.StatusForbidden, http.StatusConflict, http.StatusTooManyRequests)

	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "Setup is complete or no setup token is configured."
	conflict.Content[problemContentType].Examples = map[string]*huma.Example{
		"setupDone":         {Value: firstAdminProblem(account.ErrSetupDone)},
		"setupUnconfigured": {Value: firstAdminProblem(account.ErrSetupUnconfigured)},
	}

	forbidden := responses[strconv.Itoa(http.StatusForbidden)]
	forbidden.Description = "The setup token is invalid."
	forbidden.Content[problemContentType].Example = firstAdminProblem(account.ErrSetupTokenInvalid)

	throttled := responses[strconv.Itoa(http.StatusTooManyRequests)]
	throttled.Description = "Too many recent failed attempts from this address."
	throttled.Content[problemContentType].Example = throttleProblem(session.ErrThrottled)

	return responses
}

func firstAdminProblem(err error) error {
	switch {
	case errors.Is(err, account.ErrSetupDone):
		return newProblem(http.StatusConflict, setupDoneType, "Setup complete", "An administrator has already been created.")
	case errors.Is(err, account.ErrSetupUnconfigured):
		return newProblem(http.StatusConflict, setupUnconfiguredType, "Setup unavailable", "Configure INBOK_SETUP_TOKEN to enable setup.")
	case errors.Is(err, account.ErrSetupTokenInvalid):
		return newProblem(http.StatusForbidden, setupTokenInvalidType, "Setup token invalid", "The setup token is invalid.")
	case errors.Is(err, emailaddress.ErrInvalid):
		return invalidEmailProblem("body.email")
	default:
		return err
	}
}
