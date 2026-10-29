package httpapi

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"strconv"

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

type totpHandler struct{ service totpService }

func registerTOTP(api huma.API, service totpService) {
	handler := totpHandler{service: service}
	registerTOTPStatus(api, handler)
	registerTOTPEnrollment(api, handler)
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
