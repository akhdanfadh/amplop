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

const (
	totpPath            = "/totp"
	invalidTOTPCodeType = problemPrefix + "invalid-totp-code"
	totpNotOnType       = problemPrefix + "totp-not-on"
)

type totpService interface {
	Status(ctx context.Context, userID int64) (totp.Status, error)
	Start(ctx context.Context, user account.User) (totp.Started, error)
}

type enrollmentService interface {
	Confirm(ctx context.Context, current session.Authenticated, code string) (session.Confirmed, error)
}

type backupService interface {
	Regenerate(ctx context.Context, userID int64, code string) ([]string, error)
}

type disableService interface {
	Disable(ctx context.Context, userID int64, code string) error
}

type totpHandler struct {
	service    totpService
	enrollment enrollmentService
	backups    backupService
	disabler   disableService
}

func registerTOTP(api huma.API, service totpService, enrollment enrollmentService, backups backupService, disabler disableService) {
	handler := totpHandler{
		service:    service,
		enrollment: enrollment,
		backups:    backups,
		disabler:   disabler,
	}

	registerTOTPStatus(api, handler)
	registerTOTPEnrollment(api, handler)
	registerTOTPConfirmation(api, handler)
	registerBackupRegeneration(api, handler)
	registerTOTPDisable(api, handler)
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

const totpNotPendingType = problemPrefix + "totp-not-pending"

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

// ========================
// Backup-code regeneration
// ========================

type totpRegenerationCode struct {
	Code string `json:"code" writeOnly:"true" maxLength:"6" doc:"Fresh six-digit code from the confirmed authenticator."`
}

type totpBackupCodes struct {
	BackupCodes []string `json:"backupCodes" nullable:"false" minItems:"10" maxItems:"10" doc:"Shown once. Save these replacement codes before leaving this page."`
}

func registerBackupRegeneration(api huma.API, handler totpHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "regenerateTOTPBackupCodes",
		Method:      http.MethodPost,
		Path:        totpPath + "/backup-codes",
		Summary:     "Replace two-factor authentication backup codes",
		Description: "Requires a fresh authenticator code. Invalidates the old backup codes and returns ten replacements once. Existing sessions stay signed in.",
		Metadata:    map[string]any{accessKey: session.AccessMember},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   backupRegenerationResponses(api),
	}, handler.handleRegenerateBackupCodes)
}

func (h totpHandler) handleRegenerateBackupCodes(ctx context.Context, in *struct{ Body totpRegenerationCode }) (*output[totpBackupCodes], error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	codes, err := h.backups.Regenerate(ctx, user.ID, in.Body.Code)
	if err != nil {
		return nil, backupRegenerationProblem(err)
	}

	// The service exposes these codes only after replacement commits.
	return respondData(totpBackupCodes{BackupCodes: codes}), nil
}

func backupRegenerationResponses(api huma.API) map[string]*huma.Response {
	responses := totpStatusResponses(api)
	maps.Copy(responses, problemResponses(api, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusTooManyRequests))

	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "No confirmed authenticator is available."
	conflict.Content[problemContentType].Example = backupRegenerationProblem(totp.ErrNotOn)

	invalid := responses[strconv.Itoa(http.StatusUnprocessableEntity)]
	invalid.Description = "The request inputs or authenticator code are invalid."
	invalid.Content[problemContentType].Examples = map[string]*huma.Example{
		"invalidCode":  {Value: backupRegenerationProblem(totp.ErrInvalidCode)},
		"invalidInput": {Value: requestProblem(http.StatusUnprocessableEntity)},
	}

	throttled := responses[strconv.Itoa(http.StatusTooManyRequests)]
	throttled.Description = "Too many recent failed code attempts for this account."
	throttled.Content[problemContentType].Example = totpThrottleProblem(totp.ErrThrottled)

	return responses
}

func backupRegenerationProblem(err error) error {
	switch {
	case errors.Is(err, account.ErrUserNotFound):
		return unauthenticatedProblem()

	case errors.Is(err, totp.ErrNotOn):
		return newProblem(
			http.StatusConflict, totpNotOnType,
			"Authenticator not enabled", "Confirm an authenticator before replacing backup codes.",
		)

	case errors.Is(err, totp.ErrInvalidCode):
		return newProblem(
			http.StatusUnprocessableEntity, invalidTOTPCodeType,
			"Invalid authenticator code", "Enter a current code from your confirmed authenticator.",
			inputError{Location: "body.code", Message: "Enter a current authenticator code."},
		)

	default:
		return totpThrottleProblem(err)
	}
}

// ========================
// TOTP turn-off
// ========================

const (
	totpDisableForbiddenType = problemPrefix + "totp-disable-forbidden"
	invalidTOTPProofType     = problemPrefix + "invalid-totp-proof"
	invalidTOTPProofMessage  = "Enter a current authenticator code or an unused backup code."
)

type totpDisableCode struct {
	Code string `json:"code" writeOnly:"true" maxLength:"128" doc:"Fresh authenticator code or unused backup code."`
}

func registerTOTPDisable(api huma.API, handler totpHandler) {
	huma.Register(api, huma.Operation{
		OperationID:   "disableTOTP",
		Method:        http.MethodPost,
		Path:          totpPath + "/disable",
		Summary:       "Turn off optional two-factor authentication",
		Description:   "Members only. Requires a fresh authenticator code or an unused backup code. Removes the authenticator and all backup codes; existing sessions stay signed in.",
		DefaultStatus: http.StatusOK,
		Metadata:      map[string]any{accessKey: session.AccessMember},
		Security:      []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:     totpDisableResponses(api),
	}, handler.handleDisableTOTP)
}

func (h totpHandler) handleDisableTOTP(ctx context.Context, in *struct{ Body totpDisableCode }) (*output[totpStatus], error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	if err := h.disabler.Disable(ctx, user.ID, in.Body.Code); err != nil {
		return nil, totpDisableProblem(err)
	}

	// Deletion committed; a second status read could fail after a successful action.
	return respondData(totpStatus{Status: totp.StateOff, BackupCodesLeft: 0}), nil
}

func totpDisableResponses(api huma.API) map[string]*huma.Response {
	responses := totpStatusResponses(api)
	maps.Copy(responses, problemResponses(api, http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusTooManyRequests))

	forbidden := responses[strconv.Itoa(http.StatusForbidden)]
	forbidden.Description = "Administrators cannot turn off two-factor authentication."
	forbidden.Content[problemContentType].Example = totpDisableProblem(totp.ErrDisableForbidden)

	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "No confirmed authenticator is available."
	conflict.Content[problemContentType].Example = totpDisableProblem(totp.ErrNotOn)

	invalid := responses[strconv.Itoa(http.StatusUnprocessableEntity)]
	invalid.Description = "The request inputs or second-factor proof are invalid."
	invalid.Content[problemContentType].Examples = map[string]*huma.Example{
		"invalidProof": {Value: totpDisableProblem(totp.ErrInvalidCode)},
		"invalidInput": {Value: requestProblem(http.StatusUnprocessableEntity)},
	}

	throttled := responses[strconv.Itoa(http.StatusTooManyRequests)]
	throttled.Description = "Too many recent failed code attempts for this account."
	throttled.Content[problemContentType].Example = totpThrottleProblem(totp.ErrThrottled)

	return responses
}

func totpDisableProblem(err error) error {
	switch {
	case errors.Is(err, account.ErrUserNotFound):
		return unauthenticatedProblem()

	case errors.Is(err, totp.ErrDisableForbidden):
		return newProblem(
			http.StatusForbidden, totpDisableForbiddenType,
			"Two-factor authentication required", "Administrators must keep two-factor authentication enabled.",
		)

	case errors.Is(err, totp.ErrNotOn):
		return newProblem(
			http.StatusConflict, totpNotOnType,
			"Authenticator not enabled", "Two-factor authentication is not enabled for this account.",
		)

	case errors.Is(err, totp.ErrInvalidCode):
		return newProblem(
			http.StatusUnprocessableEntity, invalidTOTPProofType,
			"Invalid second-factor proof", invalidTOTPProofMessage,
			inputError{Location: "body.code", Message: invalidTOTPProofMessage},
		)

	default:
		return totpThrottleProblem(err)
	}
}
