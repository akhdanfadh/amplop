package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"inbok/internal/registration"
	"inbok/internal/session"
)

const (
	registrationPath         = "/registration"
	registrationSettingsPath = registrationPath + "/settings"
)

type registrationService interface {
	Settings(ctx context.Context) (registration.Settings, error)
	Save(ctx context.Context, actorID int64, state registration.State) (registration.Settings, error)
}

type registrationHandler struct {
	service registrationService
}

func registerRegistration(api huma.API, service registrationService) {
	handler := registrationHandler{service: service}

	registerGetRegistration(api, handler)
	registerRegistrationSettings(api, handler)
	registerRegistrationSave(api, handler)
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
		Summary:     "Get whether visitors can register",
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
	Status            registration.Status `json:"status" enum:"closed,open,paused" doc:"Closed: visitors cannot register. Open: anyone who can reach this inbok can register. Paused: opened, but the system email settings in use have not passed a test, so visitors cannot register until one does."`
	SystemEmailTested bool                `json:"systemEmailTested" doc:"Whether the system email settings in use passed a test, which opening registration needs."`
}

func registerRegistrationSettings(api huma.API, handler registrationHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "getRegistrationSettings",
		Method:      http.MethodGet,
		Path:        registrationSettingsPath,
		Summary:     "Get the registration settings",
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
	return registrationSettings{Status: settings.Status, SystemEmailTested: settings.SystemEmailTested}
}

// ========================
// Registration save
// ========================

const systemEmailNotTestedType = problemPrefix + "system-email-not-tested"

// newRegistrationSettings is the save request body.
type newRegistrationSettings struct {
	Open bool `json:"open" doc:"True opens registration to anyone who can reach this inbok; false closes it."`
}

func registerRegistrationSave(api huma.API, handler registrationHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "saveRegistrationSettings",
		Method:      http.MethodPut,
		Path:        registrationSettingsPath,
		Summary:     "Open or close registration",
		Description: "Opening needs system email settings that passed a test; closing always succeeds. Each save is recorded in the audit log.",
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
