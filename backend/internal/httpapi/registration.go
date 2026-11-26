package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"amplop/internal/account"
	"amplop/internal/emailaddress"
	"amplop/internal/registration"
	"amplop/internal/session"
	"amplop/internal/systememail"
)

const (
	registrationPath         = "/registration"
	registrationSettingsPath = registrationPath + "/settings"
	registrationLinksPath    = registrationPath + "/links"
	registrationRedeemPath   = registrationLinksPath + "/redeem"
)

type registrationService interface {
	Settings(ctx context.Context) (registration.Settings, error)
	Save(ctx context.Context, actorID int64, state registration.State) (registration.Settings, error)
}

type registrationRequestService interface {
	Send(ctx context.Context, email string, client netip.Addr) error
}

type registrationCompletionService interface {
	Complete(ctx context.Context, secret, email, password string) (account.User, error)
}

type registrationHandler struct {
	service    registrationService
	requests   registrationRequestService
	completion registrationCompletionService
}

func registerRegistration(api huma.API, service registrationService, requests registrationRequestService, completion registrationCompletionService) {
	handler := registrationHandler{service: service, requests: requests, completion: completion}

	registerGetRegistration(api, handler)
	registerRegistrationSettings(api, handler)
	registerRegistrationSave(api, handler)
	registerRegistrationRequest(api, handler)
	registerRegistrationComplete(api, handler)
}

// ========================
// Registration availability
// ========================

type registrationAvailability struct {
	Open bool `json:"open" doc:"Whether visitors can register now: an administrator opened registration and the system email settings in use passed a test."`
}

func registerGetRegistration(api huma.API, handler registrationHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "getRegistration",
		Method:      http.MethodGet,
		Path:        registrationPath,
		Summary:     "Get Registration Status",
		Tags:        []string{tagRegistration},
		Description: "Whether visitors can register.",
		Metadata:    map[string]any{accessKey: session.AccessPublic},
		Responses:   problemResponses(api),
	}, handler.handleGetRegistration)
}

func (h registrationHandler) handleGetRegistration(ctx context.Context, _ *struct{}) (*output[registrationAvailability], error) {
	settings, err := h.service.Settings(ctx)
	if err != nil {
		return nil, err
	}
	// Paused is closed to visitors; only administrators see why.
	return respondData(registrationAvailability{Open: settings.Status == registration.StatusOpen}), nil
}

// ========================
// Registration settings
// ========================

type registrationSettings struct {
	Status            registration.Status `json:"status" enum:"closed,open,paused" doc:"Closed: visitors cannot register. Open: anyone who can reach this amplop can register. Paused: opened, but the system email settings in use have not passed a test, so visitors cannot register until one does."`
	SystemEmailTested bool                `json:"systemEmailTested" doc:"Whether the system email settings in use passed a test, which opening registration needs."`
	// Listed here, so every client offers only what the service accepts
	// instead of restating its rule (AGENTS.md, "API contract").
	AllowedActions []registration.Action `json:"allowedActions" nullable:"false" doc:"What an administrator can ask of registration now: open (Save Registration Settings with open true) and close (with open false). open is missing until the system email settings in use pass a test. An action that would change nothing, e.g. closing registration already closed, is listed and succeeds without a change. A listed action can still be refused when the settings changed since this read. Clients ignore values they don't know."`
}

// TransformSchema lists allowedActions' values from [registration.Actions].
func (registrationSettings) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	values := make([]any, 0, len(registration.Actions))
	for _, action := range registration.Actions {
		values = append(values, string(action))
	}
	s.Properties["allowedActions"].Items.Enum = values
	return s
}

func registerRegistrationSettings(api huma.API, handler registrationHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "getRegistrationSettings",
		Method:      http.MethodGet,
		Path:        registrationSettingsPath,
		Summary:     "Get Registration Settings",
		Tags:        []string{tagRegistration},
		Metadata:    map[string]any{accessKey: session.AccessAdmin},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   adminResponses(api),
	}, handler.handleGetRegistrationSettings)
}

func (h registrationHandler) handleGetRegistrationSettings(ctx context.Context, _ *struct{}) (*output[registrationSettings], error) {
	settings, err := h.service.Settings(ctx)
	if err != nil {
		return nil, err
	}
	return respondData(registrationSettingsOf(settings)), nil
}

// registrationSettingsOf returns settings in the response's shape.
func registrationSettingsOf(settings registration.Settings) registrationSettings {
	// The contract promises a list, never null.
	actions := settings.AllowedActions
	if actions == nil {
		actions = []registration.Action{}
	}
	return registrationSettings{
		Status:            settings.Status,
		SystemEmailTested: settings.SystemEmailTested,
		AllowedActions:    actions,
	}
}

// ========================
// Registration save
// ========================

const systemEmailNotTestedType = problemPrefix + "system-email-not-tested"

// newRegistrationSettings is the save request body.
type newRegistrationSettings struct {
	Open bool `json:"open" doc:"True opens registration to anyone who can reach this amplop; false closes it."`
}

func registerRegistrationSave(api huma.API, handler registrationHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "saveRegistrationSettings",
		Method:      http.MethodPut,
		Path:        registrationSettingsPath,
		Summary:     "Save Registration Settings",
		Tags:        []string{tagRegistration},
		Description: "Opens or closes registration. Opening needs system email settings that passed a test; closing always succeeds. Each save is recorded in the audit log.",
		Metadata:    map[string]any{accessKey: session.AccessAdmin},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   registrationSaveResponses(api),
	}, handler.handleSaveRegistration)
}

func (h registrationHandler) handleSaveRegistration(ctx context.Context, in *struct{ Body newRegistrationSettings }) (*output[registrationSettings], error) {
	// The actor is the checked session's account, never a request value.
	actor := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	state := registration.StateClosed
	if in.Body.Open {
		state = registration.StateOpen
	}
	saved, err := h.service.Save(ctx, actor.ID, state)
	if err != nil {
		return nil, registrationSaveProblem(err)
	}
	return respondData(registrationSettingsOf(saved)), nil
}

func registrationSaveResponses(api huma.API) map[string]*huma.Response {
	responses := adminResponses(api, http.StatusConflict, http.StatusUnprocessableEntity)

	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "The system email settings in use have not passed a test."
	conflict.Content[problemContentType].Example = registrationSaveProblem(registration.ErrSystemEmailNotTested)

	invalid := responses[strconv.Itoa(http.StatusUnprocessableEntity)]
	invalid.Description = "The request inputs are invalid."
	invalid.Content[problemContentType].Example = requestProblem(http.StatusUnprocessableEntity)
	return responses
}

func registrationSaveProblem(err error) error {
	if errors.Is(err, registration.ErrSystemEmailNotTested) {
		return newProblem(http.StatusConflict, systemEmailNotTestedType, "System email not tested",
			"Save the system email settings and send a test message that passes before opening registration.")
	}
	return err
}

// ========================
// Registration link request
// ========================

const (
	registrationClosedType    = problemPrefix + "registration-closed"
	registrationThrottledType = problemPrefix + "registration-throttled"
)

// registrationRequestStatus is the one answer every accepted request gets.
type registrationRequestStatus string

const registrationRequested registrationRequestStatus = "requested"

// newRegistrationRequest is the request body.
type newRegistrationRequest struct {
	Email string `json:"email" format:"idn-email" minLength:"3" maxLength:"254" doc:"Letters A to Z are saved in lowercase."`
}

// registrationRequestInput combines the email with the resolved client
// address.
type registrationRequestInput struct {
	Body newRegistrationRequest
	addr netip.Addr
}

// Resolve fills the client address for throttling using Gin's proxy rules.
// Huma calls it automatically after parsing the request, before the handler.
func (in *registrationRequestInput) Resolve(ctx huma.Context) []error {
	return resolveClientAddr(ctx, &in.addr)
}

type registrationRequestResult struct {
	Status registrationRequestStatus `json:"status" enum:"requested" doc:"The same for every accepted request, so it does not tell whether the email has an account."`
}

func registerRegistrationRequest(api huma.API, handler registrationHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "requestRegistrationLink",
		Method:      http.MethodPost,
		Path:        registrationLinksPath,
		Summary:     "Send Registration Link",
		Tags:        []string{tagRegistration},
		Description: "An email without an account gets a link that expires in 24 hours; an email with an account gets a notice instead. " +
			"An email that got 3 messages in the last hour or 5 in 24 hours gets nothing. While sending works, the response is the same in every case. " +
			"One client may ask 10 times in 15 minutes, and everyone together 50 times an hour.",
		Metadata:    map[string]any{accessKey: session.AccessPublic},
		Middlewares: huma.Middlewares{withNoStore},
		Responses:   registrationRequestResponses(api),
	}, handler.handleRequestRegistrationLink)
}

func (h registrationHandler) handleRequestRegistrationLink(ctx context.Context, in *registrationRequestInput) (*output[registrationRequestResult], error) {
	if err := h.requests.Send(ctx, in.Body.Email, in.addr); err != nil {
		return nil, registrationRequestProblem(err)
	}
	return respondData(registrationRequestResult{Status: registrationRequested}), nil
}

func registrationRequestResponses(api huma.API) map[string]*huma.Response {
	responses := problemResponses(api, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusTooManyRequests, http.StatusBadGateway)

	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "Registration is closed, or paused until the system email settings pass a test."
	conflict.Content[problemContentType].Example = registrationRequestProblem(registration.ErrClosed)

	invalid := responses[strconv.Itoa(http.StatusUnprocessableEntity)]
	invalid.Description = "The email is not valid."
	invalid.Content[problemContentType].Example = registrationRequestProblem(emailaddress.ErrInvalid)

	throttled := responses[strconv.Itoa(http.StatusTooManyRequests)]
	throttled.Description = "This client, or everyone together, asked for too many registration emails recently."
	throttled.Content[problemContentType].Example = registrationRequestProblem(registration.ErrThrottled)

	badGateway := responses[strconv.Itoa(http.StatusBadGateway)]
	badGateway.Description = "Sending failed or timed out. A link was saved and counts toward the limits; after a timeout the message may still arrive."
	badGateway.Content[problemContentType].Example = registrationRequestProblem(&systememail.SendError{})
	return responses
}

func registrationRequestProblem(err error) error {
	switch {
	case errors.Is(err, emailaddress.ErrInvalid):
		return invalidEmailProblem("body.email")
	case errors.Is(err, registration.ErrClosed):
		return newProblem(http.StatusConflict, registrationClosedType, "Registration not open", "Registration is not open right now.")
	case errors.Is(err, registration.ErrThrottled):
		return retryAfterProblem(newProblem(http.StatusTooManyRequests, registrationThrottledType, "Too many registration requests", "Too many registration requests were made recently."), err)
	}
	// The server's reason stays in the log; it can name the username.
	if _, ok := errors.AsType[*systememail.SendError](err); ok {
		return emailNotSentProblem()
	}
	return err
}

// ========================
// Registration completion
// ========================

const (
	registrationLinkInvalidType   = problemPrefix + "registration-link-invalid"
	registrationAccountExistsType = problemPrefix + "registration-account-exists"
)

// newMember is the completion request body.
type newMember struct {
	Secret   string `json:"secret" writeOnly:"true" doc:"The text after # in the emailed link."`
	Email    string `json:"email" format:"idn-email" minLength:"3" maxLength:"254" doc:"The email the link was sent to, typed by the person using it. Letters A to Z match in either case."`
	Password string `json:"password" writeOnly:"true" minLength:"15" maxLength:"128"`
}

type registeredMember struct {
	ID    int64  `json:"id"`
	Email string `json:"email" doc:"The email the link was sent to, for signing in."`
}

func registerRegistrationComplete(api huma.API, handler registrationHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "redeemRegistrationLink",
		Method:      http.MethodPost,
		Path:        registrationRedeemPath,
		Summary:     "Complete Registration",
		Tags:        []string{tagRegistration},
		Description: "Creates a member whose email, the one the link was sent to, is verified, when the email given is that one. " +
			"Uses up every link sent to that email. Needs no session and signs no one in.",
		Metadata:      map[string]any{accessKey: session.AccessPublic},
		Middlewares:   huma.Middlewares{withNoStore},
		DefaultStatus: http.StatusCreated,
		Responses:     registrationCompleteResponses(api),
	}, handler.handleRedeemRegistrationLink)
}

func (h registrationHandler) handleRedeemRegistrationLink(ctx context.Context, in *struct{ Body newMember }) (*output[registeredMember], error) {
	user, err := h.completion.Complete(ctx, in.Body.Secret, in.Body.Email, in.Body.Password)
	if err != nil {
		return nil, registrationCompleteProblem(err)
	}
	return respondData(registeredMember{ID: user.ID, Email: user.Email}), nil
}

func registrationCompleteResponses(api huma.API) map[string]*huma.Response {
	responses := problemResponses(api, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity)

	notFound := responses[strconv.Itoa(http.StatusNotFound)]
	notFound.Description = "The link is unknown, used, or expired."
	notFound.Content[problemContentType].Example = registrationCompleteProblem(registration.ErrLinkInvalid)

	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "Registration is closed or paused, or the email already has an account."
	conflict.Content[problemContentType].Examples = map[string]*huma.Example{
		"closed":        {Value: registrationCompleteProblem(registration.ErrClosed)},
		"accountExists": {Value: registrationCompleteProblem(registration.ErrAccountExists)},
	}

	invalid := responses[strconv.Itoa(http.StatusUnprocessableEntity)]
	invalid.Description = "The request inputs are invalid, or the link was sent to a different email."
	invalid.Content[problemContentType].Examples = map[string]*huma.Example{
		"password":      {Value: registrationCompleteProblem(account.ErrPasswordInvalid)},
		"emailMismatch": {Value: registrationCompleteProblem(registration.ErrEmailMismatch)},
	}
	return responses
}

func registrationCompleteProblem(err error) error {
	switch {
	// Huma's length check refuses almost every such password first; the
	// service's rule decides the rest.
	case errors.Is(err, account.ErrPasswordInvalid):
		return invalidPasswordProblem("body.password")
	case errors.Is(err, emailaddress.ErrInvalid):
		return invalidEmailProblem("body.email")
	case errors.Is(err, registration.ErrEmailMismatch):
		return linkEmailMismatchProblem()
	case errors.Is(err, registration.ErrLinkInvalid):
		return newProblem(http.StatusNotFound, registrationLinkInvalidType, "Link not valid", "This link has expired, was already used, or is not valid. Ask for a new one.")
	case errors.Is(err, registration.ErrClosed):
		// Pausing keeps links, so this one may work again; closing deletes
		// them, so a new one is needed.
		return newProblem(http.StatusConflict, registrationClosedType, "Registration not open",
			"Registration is not open right now. Try this link again later; if it no longer works, ask for a new one.")
	case errors.Is(err, registration.ErrAccountExists):
		return newProblem(http.StatusConflict, registrationAccountExistsType, "Account exists", "This email already has an account. Sign in instead.")
	}
	return err
}
