package httpapi

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"inbok/internal/account"
	"inbok/internal/session"
	"inbok/internal/totp"
)

const totpPath = "/totp"

type totpService interface {
	Status(ctx context.Context, userID int64) (totp.Status, error)
	Start(ctx context.Context, user account.User) (totp.Started, error)
}

type enrollmentService interface {
	Confirm(ctx context.Context, current session.Authenticated, code string) (session.Confirmed, error)
}

type totpHandler struct {
	service    totpService
	enrollment enrollmentService
}

func registerTOTP(api huma.API, service totpService, enrollment enrollmentService) {
	handler := totpHandler{service: service, enrollment: enrollment}
	registerTOTPStatus(api, handler)
	registerTOTPEnrollment(api, handler)
	registerTOTPConfirmation(api, handler)
}

// ========================
// TOTP status
// ========================

type totpStatus struct {
	Status          totp.State `json:"status" enum:"off,pending,on"`
	BackupCodesLeft int        `json:"backupCodesLeft" minimum:"0" maximum:"10"`
}

func registerTOTPStatus(api huma.API, handler totpHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "getTOTPStatus",
		Method:      http.MethodGet,
		Path:        totpPath,
		Summary:     "Get two-factor authentication status",
		Metadata:    map[string]any{accessKey: session.AccessMember},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   totpStatusResponses(api),
	}, handler.handleGetTOTPStatus)
}

func (h totpHandler) handleGetTOTPStatus(ctx context.Context, _ *struct{}) (*output[totpStatus], error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	status, err := h.service.Status(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	return respondData(totpStatus{Status: status.State, BackupCodesLeft: status.BackupCodesLeft}), nil
}

func totpStatusResponses(api huma.API) map[string]*huma.Response {
	responses := problemResponses(api, http.StatusUnauthorized)
	unauthorized := responses[strconv.Itoa(http.StatusUnauthorized)]
	unauthorized.Description = "No valid session identifies a current account."
	unauthorized.Content[problemContentType].Example = unauthenticatedProblem()
	return responses
}

// ========================
// TOTP enrollment
// ========================

const totpAlreadyOnType = problemPrefix + "totp-already-on"

type totpEnrollment struct {
	Secret string `json:"secret" doc:"Manual authenticator setup secret. Keep private."`
	URI    string `json:"uri" doc:"Authenticator setup URI containing the secret. Keep private."`
}

func registerTOTPEnrollment(api huma.API, handler totpHandler) {
	huma.Register(api, huma.Operation{
		OperationID:   "startTOTPEnrollment",
		Method:        http.MethodPost,
		Path:          totpPath,
		Summary:       "Start or restart two-factor authentication enrollment",
		DefaultStatus: http.StatusCreated,
		Metadata:      map[string]any{accessKey: session.AccessMember},
		Security:      []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:     totpEnrollmentResponses(api),
	}, handler.handleStartTOTPEnrollment)
}

func (h totpHandler) handleStartTOTPEnrollment(ctx context.Context, _ *struct{}) (*output[totpEnrollment], error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	started, err := h.service.Start(ctx, user)
	if err != nil {
		return nil, totpEnrollmentProblem(err)
	}
	return respondData(totpEnrollment{Secret: started.Secret, URI: started.URI}), nil
}

func totpEnrollmentResponses(api huma.API) map[string]*huma.Response {
	responses := totpStatusResponses(api)
	maps.Copy(responses, problemResponses(api, http.StatusConflict))
	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "Two-factor authentication is already on."
	conflict.Content[problemContentType].Example = totpEnrollmentProblem(totp.ErrAlreadyOn)
	return responses
}

func totpEnrollmentProblem(err error) error {
	if errors.Is(err, totp.ErrAlreadyOn) {
		return newProblem(http.StatusConflict, totpAlreadyOnType, "Two-factor authentication already on", "An authenticator is already confirmed for this account.")
	}
	return err
}

// ========================
// TOTP confirmation
// ========================

const (
	totpNotPendingType  = problemPrefix + "totp-not-pending"
	invalidTOTPCodeType = problemPrefix + "invalid-totp-code"
)

type totpConfirmationCode struct {
	// Keep leading zeros; malformed codes within this bound count as attempts.
	Code string `json:"code" writeOnly:"true" maxLength:"6" doc:"Six-digit code from the pending authenticator."`
}

type totpConfirmation struct {
	BackupCodes []string `json:"backupCodes" nullable:"false" minItems:"10" maxItems:"10" doc:"Shown once. Save these codes before leaving this page."`
}

type totpConfirmationOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      response[totpConfirmation]
}

func registerTOTPConfirmation(api huma.API, handler totpHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "confirmTOTPEnrollment",
		Method:      http.MethodPost,
		Path:        totpPath + "/confirm",
		Summary:     "Confirm two-factor authentication enrollment",
		Description: "Activates the authenticator, ends all existing sessions, and issues a replacement session cookie. Backup codes are returned once.",
		Metadata:    map[string]any{accessKey: session.AccessMember},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   totpConfirmationResponses(api),
	}, handler.handleConfirmTOTPEnrollment)
}

func (h totpHandler) handleConfirmTOTPEnrollment(ctx context.Context, in *struct{ Body totpConfirmationCode }) (*totpConfirmationOutput, error) {
	current := ctx.Value(sessionKey{}).(session.Authenticated)
	confirmed, err := h.enrollment.Confirm(ctx, current, in.Body.Code)
	if err != nil {
		return nil, totpConfirmationProblem(err)
	}

	// Confirm returns only after the factor, backup hashes, and session commit.
	return &totpConfirmationOutput{
		SetCookie: sessionCookie(confirmed.Session.Secret, int(time.Until(confirmed.Session.State.ExpiresAt)/time.Second)),
		Body:      respondData(totpConfirmation{BackupCodes: confirmed.BackupCodes}).Body,
	}, nil
}

func totpConfirmationResponses(api huma.API) map[string]*huma.Response {
	responses := totpStatusResponses(api)
	maps.Copy(responses, problemResponses(api, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusTooManyRequests))

	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "Enrollment has not started or the authenticator is already confirmed."
	conflict.Content[problemContentType].Examples = map[string]*huma.Example{
		"notPending": {Value: totpConfirmationProblem(totp.ErrNotPending)},
		"alreadyOn":  {Value: totpConfirmationProblem(totp.ErrAlreadyOn)},
	}

	invalid := responses[strconv.Itoa(http.StatusUnprocessableEntity)]
	invalid.Description = "The request inputs or authenticator code are invalid."
	invalid.Content[problemContentType].Examples = map[string]*huma.Example{
		"invalidCode":  {Value: totpConfirmationProblem(totp.ErrInvalidCode)},
		"invalidInput": {Value: requestProblem(http.StatusUnprocessableEntity)},
	}

	throttled := responses[strconv.Itoa(http.StatusTooManyRequests)]
	throttled.Description = "Too many recent failed code attempts for this account."
	throttled.Content[problemContentType].Example = totpConfirmationProblem(totp.ErrThrottled)
	return responses
}

func totpConfirmationProblem(err error) error {
	switch {
	case errors.Is(err, session.ErrUnauthenticated):
		return unauthenticatedProblem()
	case errors.Is(err, totp.ErrNotPending):
		return newProblem(http.StatusConflict, totpNotPendingType, "Enrollment not started", "Start authenticator enrollment before confirming a code.")
	case errors.Is(err, totp.ErrInvalidCode):
		return newProblem(http.StatusUnprocessableEntity, invalidTOTPCodeType, "Invalid authenticator code", "Enter a current code from the pending authenticator.",
			inputError{Location: "body.code", Message: "Enter a current authenticator code."})
	case errors.Is(err, totp.ErrThrottled):
		return totpThrottleProblem(err)
	default:
		return totpEnrollmentProblem(err)
	}
}
