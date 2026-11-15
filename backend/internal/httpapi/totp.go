package httpapi

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/netip"
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

	// A refused code's detail is also its field's message, so a form that
	// shows only the field loses nothing. Enrollment names the new entry:
	// an app may still hold one from an earlier enrollment, whose codes fail.
	pendingTOTPCodeMessage   = "Enter the current code from the authenticator entry you just added."
	confirmedTOTPCodeMessage = "Enter a current code from your authenticator app."
)

type totpService interface {
	Status(ctx context.Context, userID int64) (totp.Status, error)
	Start(ctx context.Context, user account.User, password string, addr netip.Addr) (totp.Started, error)
	VerifyCode(ctx context.Context, userID int64, code string) (totp.Codes, error)
	ShowWaiting(ctx context.Context, user account.User, password string, addr netip.Addr) (totp.Codes, error)
	Cancel(ctx context.Context, userID int64) error
}

type enrollmentService interface {
	Finish(ctx context.Context, current session.Authenticated, token string, device session.Device) (session.Finished, error)
}

type backupService interface {
	Replace(ctx context.Context, userID int64, code string) (totp.Codes, error)
	Finish(ctx context.Context, userID int64, token string) (int, error)
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
	registerTOTPCancellation(api, handler)
	registerTOTPCodeCheck(api, handler)
	registerTOTPFinish(api, handler)
	registerWaitingBackupCodes(api, handler)
	registerBackupReplacement(api, handler)
	registerBackupReplacementFinish(api, handler)
	registerTOTPDisable(api, handler)
}

// ========================
// TOTP status
// ========================

type totpStatus struct {
	Status             totp.State `json:"status" enum:"off,pending,expired,on" doc:"pending: enrollment started; expired: an unfinished enrollment ran out of time."`
	BackupCodesLeft    int        `json:"backupCodesLeft" minimum:"0" maximum:"10"`
	BackupCodesWaiting bool       `json:"backupCodesWaiting" doc:"Whether new backup codes wait for Finish: during enrollment after the code check, or while replacing codes."`
}

func registerTOTPStatus(api huma.API, handler totpHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "getTOTPStatus",
		Method:      http.MethodGet,
		Path:        totpPath,
		Summary:     "Get Two-Factor Status",
		Tags:        []string{tagTwoFactor},
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
	return respondData(totpStatus{Status: status.State, BackupCodesLeft: status.BackupCodesLeft, BackupCodesWaiting: status.BackupCodesWaiting}), nil
}

func totpStatusResponses(api huma.API) map[string]*huma.Response {
	responses := problemResponses(api, http.StatusUnauthorized)
	unauthorized := responses[strconv.Itoa(http.StatusUnauthorized)]
	unauthorized.Description = unauthenticatedDescription
	unauthorized.Content[problemContentType].Example = unauthenticatedProblem()
	return responses
}

// ========================
// Password check
// ========================

const invalidPasswordType = problemPrefix + "invalid-password"

// invalidPasswordMessage is both the detail and the field message, so a form
// that shows only the field loses nothing.
const invalidPasswordMessage = "Enter your current password."

// passwordProof is a body carrying the account's current password.
type passwordProof struct {
	Password string `json:"password" writeOnly:"true" maxLength:"128" doc:"The account's current password."`
}

// passwordProofInput adds the client address the password check counts
// attempts by.
type passwordProofInput struct {
	addr netip.Addr
	Body passwordProof
}

// Resolve fills the client address for throttling using Gin's proxy rules.
func (in *passwordProofInput) Resolve(ctx huma.Context) []error {
	return resolveClientAddr(ctx, &in.addr)
}

// passwordProofResponses adds the password check's failures to responses.
func passwordProofResponses(responses map[string]*huma.Response) {
	invalid := responses[strconv.Itoa(http.StatusUnprocessableEntity)]
	invalid.Description = "The request inputs or the password are invalid."
	invalid.Content[problemContentType].Examples = map[string]*huma.Example{
		"invalidPassword": {Value: passwordProblem(account.ErrInvalidCredentials)},
		"invalidInput":    {Value: requestProblem(http.StatusUnprocessableEntity)},
	}

	throttled := responses[strconv.Itoa(http.StatusTooManyRequests)]
	throttled.Description = "Too many recent wrong passwords for this account from this address."
	throttled.Content[problemContentType].Example = throttleProblem(session.ErrThrottled)
}

// passwordProblem maps the password check's failures, or returns err
// unchanged.
func passwordProblem(err error) error {
	switch {
	case errors.Is(err, account.ErrInvalidCredentials):
		return newProblem(http.StatusUnprocessableEntity, invalidPasswordType, "Incorrect password", invalidPasswordMessage,
			inputError{Location: "body.password", Message: invalidPasswordMessage})
	// The account was deactivated after its session was checked.
	case errors.Is(err, account.ErrDeactivated), errors.Is(err, account.ErrUserNotFound):
		return unauthenticatedProblem()
	}
	return throttleProblem(err)
}

// ========================
// TOTP enrollment
// ========================

const (
	totpAlreadyOnType         = problemPrefix + "totp-already-on"
	totpNotPendingType        = problemPrefix + "totp-not-pending"
	totpEnrollmentExpiredType = problemPrefix + "totp-enrollment-expired"
	totpCodeCheckedType       = problemPrefix + "totp-code-checked"
	notWaitingType            = problemPrefix + "backup-codes-not-waiting"
)

type totpEnrollment struct {
	Secret string `json:"secret" doc:"Manual authenticator enrollment secret. Keep private."`
	URI    string `json:"uri" doc:"Authenticator enrollment URI containing the secret. Keep private."`
}

func registerTOTPEnrollment(api huma.API, handler totpHandler) {
	huma.Register(api, huma.Operation{
		OperationID:   "startTOTPEnrollment",
		Method:        http.MethodPost,
		Path:          totpPath,
		Summary:       "Start Two-Factor Enrollment",
		Tags:          []string{tagTwoFactor},
		Description:   "Checks the password, then starts an enrollment, or returns the same secret for one started in the last 15 minutes. An older unfinished enrollment is replaced. Both answer 201. Wrong passwords count toward the sign-in limits.",
		DefaultStatus: http.StatusCreated,
		Metadata:      map[string]any{accessKey: session.AccessMember},
		Security:      []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:     totpEnrollmentResponses(api),
	}, handler.handleStartTOTPEnrollment)
}

func (h totpHandler) handleStartTOTPEnrollment(ctx context.Context, in *passwordProofInput) (*output[totpEnrollment], error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	started, err := h.service.Start(ctx, user, in.Body.Password, in.addr)
	if err != nil {
		return nil, totpEnrollmentProblem(err)
	}
	return respondData(totpEnrollment{Secret: started.Secret, URI: started.URI}), nil
}

func totpEnrollmentResponses(api huma.API) map[string]*huma.Response {
	responses := totpStatusResponses(api)
	maps.Copy(responses, problemResponses(api, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusTooManyRequests))
	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "Two-factor authentication is already on, or the enrollment's code was already checked."
	conflict.Content[problemContentType].Examples = map[string]*huma.Example{
		"alreadyOn":   {Value: totpEnrollmentProblem(totp.ErrAlreadyOn)},
		"codeChecked": {Value: totpEnrollmentProblem(totp.ErrCodeChecked)},
	}
	passwordProofResponses(responses)
	return responses
}

// totpEnrollmentProblem maps enrollment's expected failures, then the password
// check's.
func totpEnrollmentProblem(err error) error {
	switch {
	// The account was deleted or deactivated after its session was checked.
	case errors.Is(err, account.ErrUserNotFound), errors.Is(err, session.ErrUnauthenticated):
		return unauthenticatedProblem()
	case errors.Is(err, totp.ErrAlreadyOn):
		return newProblem(http.StatusConflict, totpAlreadyOnType, "Two-factor authentication already on", "An authenticator is already confirmed for this account.")
	case errors.Is(err, totp.ErrNotPending):
		return newProblem(http.StatusConflict, totpNotPendingType, "Enrollment not started", "Start two-factor setup first.")
	case errors.Is(err, totp.ErrEnrollmentExpired):
		return newProblem(http.StatusConflict, totpEnrollmentExpiredType, "Enrollment expired", "This setup expired after 15 minutes. Start again.")
	case errors.Is(err, totp.ErrCodeChecked):
		return newProblem(http.StatusConflict, totpCodeCheckedType, "Code already checked", "Your backup codes are waiting. Show them again to finish setup.")
	case errors.Is(err, totp.ErrNotWaiting):
		return notWaitingProblem()
	}
	return passwordProblem(err)
}

// notWaitingProblem is the answer when no backup codes wait for a token.
func notWaitingProblem() *problem {
	return newProblem(http.StatusConflict, notWaitingType, "Backup codes not waiting", "These backup codes are no longer waiting to be saved.")
}

// ========================
// TOTP enrollment cancellation
// ========================

func registerTOTPCancellation(api huma.API, handler totpHandler) {
	huma.Register(api, huma.Operation{
		OperationID:   "cancelTOTPEnrollment",
		Method:        http.MethodDelete,
		Path:          totpPath + "/enrollment",
		Summary:       "Cancel Two-Factor Enrollment",
		Tags:          []string{tagTwoFactor},
		Description:   "Removes an unfinished enrollment and any backup codes waiting. Repeating cancellation succeeds if no factor remains. Confirmed authenticators cannot be canceled; existing sessions stay signed in.",
		DefaultStatus: http.StatusOK,
		Metadata:      map[string]any{accessKey: session.AccessMember},
		Security:      []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:     totpCancellationResponses(api),
	}, handler.handleCancelTOTPEnrollment)
}

func (h totpHandler) handleCancelTOTPEnrollment(ctx context.Context, _ *struct{}) (*output[totpStatus], error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	if err := h.service.Cancel(ctx, user.ID); err != nil {
		return nil, totpEnrollmentProblem(err)
	}

	// The transaction already established off; avoid a second read after commit.
	return respondData(totpStatus{Status: totp.StateOff}), nil
}

func totpCancellationResponses(api huma.API) map[string]*huma.Response {
	responses := totpStatusResponses(api)
	maps.Copy(responses, problemResponses(api, http.StatusConflict))
	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "Two-factor authentication is already on."
	conflict.Content[problemContentType].Example = totpEnrollmentProblem(totp.ErrAlreadyOn)
	return responses
}

// ========================
// TOTP enrollment code check
// ========================

type totpEnrollmentCode struct {
	// Keep leading zeros; malformed codes within this bound count as attempts.
	Code string `json:"code" writeOnly:"true" maxLength:"6" doc:"Six-digit code from the authenticator being set up."`
}

type waitingBackupCodes struct {
	BackupCodes []string `json:"backupCodes" nullable:"false" minItems:"10" maxItems:"10" doc:"Not active yet. Save these codes, then send the token to Finish."`
	Token       string   `json:"token" doc:"One-time token that activates these codes. Keep private; showing the codes again replaces it."`
}

func registerTOTPCodeCheck(api huma.API, handler totpHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "checkTOTPEnrollmentCode",
		Method:      http.MethodPost,
		Path:        totpPath + "/enrollment/code",
		Summary:     "Check Two-Factor Enrollment Code",
		Tags:        []string{tagTwoFactor},
		Description: "Checks a code from the authenticator being set up, then returns ten backup codes and a one-time token. Two-factor authentication stays off until Finish Two-Factor Enrollment.",
		Metadata:    map[string]any{accessKey: session.AccessMember},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   totpCodeCheckResponses(api),
	}, handler.handleCheckTOTPEnrollmentCode)
}

func (h totpHandler) handleCheckTOTPEnrollmentCode(ctx context.Context, in *struct{ Body totpEnrollmentCode }) (*output[waitingBackupCodes], error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	codes, err := h.service.VerifyCode(ctx, user.ID, in.Body.Code)
	if err != nil {
		return nil, totpCodeCheckProblem(err)
	}
	return respondData(waitingBackupCodes{BackupCodes: codes.Codes, Token: codes.Token}), nil
}

func totpCodeCheckResponses(api huma.API) map[string]*huma.Response {
	responses := totpStatusResponses(api)
	maps.Copy(responses, problemResponses(api, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusTooManyRequests))

	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "No live enrollment awaits a code: none started, it expired, its code was already checked, or two-factor authentication is on."
	conflict.Content[problemContentType].Examples = map[string]*huma.Example{
		"notPending":        {Value: totpCodeCheckProblem(totp.ErrNotPending)},
		"enrollmentExpired": {Value: totpCodeCheckProblem(totp.ErrEnrollmentExpired)},
		"codeChecked":       {Value: totpCodeCheckProblem(totp.ErrCodeChecked)},
		"alreadyOn":         {Value: totpCodeCheckProblem(totp.ErrAlreadyOn)},
	}

	invalid := responses[strconv.Itoa(http.StatusUnprocessableEntity)]
	invalid.Description = "The request inputs or authenticator code are invalid."
	invalid.Content[problemContentType].Examples = map[string]*huma.Example{
		"invalidCode":  {Value: totpCodeCheckProblem(totp.ErrInvalidCode)},
		"invalidInput": {Value: requestProblem(http.StatusUnprocessableEntity)},
	}

	throttled := responses[strconv.Itoa(http.StatusTooManyRequests)]
	throttled.Description = "Too many recent failed code attempts for this account."
	throttled.Content[problemContentType].Example = totpThrottleProblem(totp.ErrThrottled)
	return responses
}

func totpCodeCheckProblem(err error) error {
	switch {
	case errors.Is(err, totp.ErrInvalidCode):
		return newProblem(http.StatusUnprocessableEntity, invalidTOTPCodeType, "Invalid authenticator code", pendingTOTPCodeMessage,
			inputError{Location: "body.code", Message: pendingTOTPCodeMessage})
	case errors.Is(err, totp.ErrThrottled):
		return totpThrottleProblem(err)
	}
	return totpEnrollmentProblem(err)
}

// ========================
// TOTP enrollment finish
// ========================

type backupCodesToken struct {
	// randomsecret tokens are 43 characters; 64 bounds the body (inbok's choice).
	Token string `json:"token" writeOnly:"true" maxLength:"64" doc:"The one-time token that came with the waiting backup codes."`
}

// totpFinishInput adds the device details the replacement session keeps.
type totpFinishInput struct {
	device session.Device
	Body   backupCodesToken
}

// Resolve reads the device details the replacement session keeps.
func (in *totpFinishInput) Resolve(ctx huma.Context) []error {
	return resolveDevice(ctx, &in.device)
}

type totpFinishOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      response[totpStatus]
}

func registerTOTPFinish(api huma.API, handler totpHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "finishTOTPEnrollment",
		Method:      http.MethodPost,
		Path:        totpPath + "/enrollment/finish",
		Summary:     "Finish Two-Factor Enrollment",
		Tags:        []string{tagTwoFactor},
		Description: "Call once the person has saved the backup codes. Turns two-factor authentication on, makes the codes active, ends all existing sessions, and issues a replacement session cookie.",
		Metadata:    map[string]any{accessKey: session.AccessMember},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   totpFinishResponses(api),
	}, handler.handleFinishTOTPEnrollment)
}

func (h totpHandler) handleFinishTOTPEnrollment(ctx context.Context, in *totpFinishInput) (*totpFinishOutput, error) {
	current := ctx.Value(sessionKey{}).(session.Authenticated)
	finished, err := h.enrollment.Finish(ctx, current, in.Body.Token, in.device)
	if err != nil {
		return nil, totpEnrollmentProblem(err)
	}

	// Finish returns only after the factor, backup codes, and session commit.
	return &totpFinishOutput{
		SetCookie: sessionCookie(finished.Secret, int(time.Until(finished.State.ExpiresAt)/time.Second)),
		Body:      respondData(totpStatus{Status: totp.StateOn, BackupCodesLeft: finished.BackupCodesLeft}).Body,
	}, nil
}

func totpFinishResponses(api huma.API) map[string]*huma.Response {
	responses := totpStatusResponses(api)
	maps.Copy(responses, problemResponses(api, http.StatusConflict))
	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "No live enrollment has codes waiting for this token, or two-factor authentication is already on."
	conflict.Content[problemContentType].Examples = map[string]*huma.Example{
		"notPending":        {Value: totpEnrollmentProblem(totp.ErrNotPending)},
		"enrollmentExpired": {Value: totpEnrollmentProblem(totp.ErrEnrollmentExpired)},
		"notWaiting":        {Value: totpEnrollmentProblem(totp.ErrNotWaiting)},
		"alreadyOn":         {Value: totpEnrollmentProblem(totp.ErrAlreadyOn)},
	}
	return responses
}

// ========================
// Waiting backup codes
// ========================

func registerWaitingBackupCodes(api huma.API, handler totpHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "showWaitingTOTPBackupCodes",
		Method:      http.MethodPost,
		Path:        totpPath + "/backup-codes/waiting",
		Summary:     "Show Waiting Backup Codes",
		Tags:        []string{tagTwoFactor},
		Description: "Checks the password, then returns an unfinished enrollment's backup codes, waiting to be saved, with a new one-time token that replaces the earlier one. Codes made by Replace Backup Codes are never shown again: once two-factor authentication is on, only a fresh authenticator code makes new ones. Wrong passwords count toward the sign-in limits.",
		Metadata:    map[string]any{accessKey: session.AccessMember},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   waitingBackupCodesResponses(api),
	}, handler.handleShowWaitingTOTPBackupCodes)
}

func (h totpHandler) handleShowWaitingTOTPBackupCodes(ctx context.Context, in *passwordProofInput) (*output[waitingBackupCodes], error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	codes, err := h.service.ShowWaiting(ctx, user, in.Body.Password, in.addr)
	if err != nil {
		return nil, totpEnrollmentProblem(err)
	}
	return respondData(waitingBackupCodes{BackupCodes: codes.Codes, Token: codes.Token}), nil
}

func waitingBackupCodesResponses(api huma.API) map[string]*huma.Response {
	responses := totpStatusResponses(api)
	maps.Copy(responses, problemResponses(api, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusTooManyRequests))
	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "No backup codes are waiting, the enrollment they belong to expired, or two-factor authentication is on."
	conflict.Content[problemContentType].Examples = map[string]*huma.Example{
		"notWaiting":        {Value: totpEnrollmentProblem(totp.ErrNotWaiting)},
		"enrollmentExpired": {Value: totpEnrollmentProblem(totp.ErrEnrollmentExpired)},
		"alreadyOn":         {Value: totpEnrollmentProblem(totp.ErrAlreadyOn)},
	}
	passwordProofResponses(responses)
	return responses
}

// ========================
// Backup-code replacement
// ========================

type totpReplacementCode struct {
	Code string `json:"code" writeOnly:"true" maxLength:"6" doc:"Fresh six-digit code from the confirmed authenticator."`
}

func registerBackupReplacement(api huma.API, handler totpHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "replaceTOTPBackupCodes",
		Method:      http.MethodPost,
		Path:        totpPath + "/backup-codes",
		Summary:     "Replace Backup Codes",
		Tags:        []string{tagTwoFactor},
		Description: "Requires a fresh authenticator code. Returns ten new backup codes and a one-time token, in this answer only: nothing shows them again. The old codes keep working until Finish Backup Code Replacement, within 15 minutes. Existing sessions stay signed in.",
		Metadata:    map[string]any{accessKey: session.AccessMember},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   backupReplacementResponses(api),
	}, handler.handleReplaceTOTPBackupCodes)
}

func (h totpHandler) handleReplaceTOTPBackupCodes(ctx context.Context, in *struct{ Body totpReplacementCode }) (*output[waitingBackupCodes], error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	codes, err := h.backups.Replace(ctx, user.ID, in.Body.Code)
	if err != nil {
		return nil, backupReplacementProblem(err)
	}
	return respondData(waitingBackupCodes{BackupCodes: codes.Codes, Token: codes.Token}), nil
}

func backupReplacementResponses(api huma.API) map[string]*huma.Response {
	responses := totpStatusResponses(api)
	maps.Copy(responses, problemResponses(api, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusTooManyRequests))

	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "No confirmed authenticator is available."
	conflict.Content[problemContentType].Example = backupReplacementProblem(totp.ErrNotOn)

	invalid := responses[strconv.Itoa(http.StatusUnprocessableEntity)]
	invalid.Description = "The request inputs or authenticator code are invalid."
	invalid.Content[problemContentType].Examples = map[string]*huma.Example{
		"invalidCode":  {Value: backupReplacementProblem(totp.ErrInvalidCode)},
		"invalidInput": {Value: requestProblem(http.StatusUnprocessableEntity)},
	}

	throttled := responses[strconv.Itoa(http.StatusTooManyRequests)]
	throttled.Description = "Too many recent failed code attempts for this account."
	throttled.Content[problemContentType].Example = totpThrottleProblem(totp.ErrThrottled)

	return responses
}

func backupReplacementProblem(err error) error {
	switch {
	case errors.Is(err, account.ErrUserNotFound):
		return unauthenticatedProblem()

	case errors.Is(err, totp.ErrNotOn):
		return newProblem(
			http.StatusConflict, totpNotOnType,
			"Authenticator not enabled", "Turn on two-factor authentication before replacing backup codes.",
		)

	case errors.Is(err, totp.ErrInvalidCode):
		return newProblem(
			http.StatusUnprocessableEntity, invalidTOTPCodeType,
			"Invalid authenticator code", confirmedTOTPCodeMessage,
			inputError{Location: "body.code", Message: confirmedTOTPCodeMessage},
		)

	case errors.Is(err, totp.ErrNotWaiting):
		return notWaitingProblem()

	default:
		return totpThrottleProblem(err)
	}
}

func registerBackupReplacementFinish(api huma.API, handler totpHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "finishTOTPBackupCodeReplacement",
		Method:      http.MethodPost,
		Path:        totpPath + "/backup-codes/finish",
		Summary:     "Finish Backup Code Replacement",
		Tags:        []string{tagTwoFactor},
		Description: "Call once the person has saved the new backup codes. Makes them active and ends the old ones. Existing sessions stay signed in.",
		Metadata:    map[string]any{accessKey: session.AccessMember},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   backupReplacementFinishResponses(api),
	}, handler.handleFinishTOTPBackupCodeReplacement)
}

func (h totpHandler) handleFinishTOTPBackupCodeReplacement(ctx context.Context, in *struct{ Body backupCodesToken }) (*output[totpStatus], error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	active, err := h.backups.Finish(ctx, user.ID, in.Body.Token)
	if err != nil {
		return nil, backupReplacementProblem(err)
	}
	return respondData(totpStatus{Status: totp.StateOn, BackupCodesLeft: active}), nil
}

func backupReplacementFinishResponses(api huma.API) map[string]*huma.Response {
	responses := totpStatusResponses(api)
	maps.Copy(responses, problemResponses(api, http.StatusConflict))
	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "No new backup codes wait for this token, or two-factor authentication is off."
	conflict.Content[problemContentType].Examples = map[string]*huma.Example{
		"notWaiting": {Value: backupReplacementProblem(totp.ErrNotWaiting)},
		"notOn":      {Value: backupReplacementProblem(totp.ErrNotOn)},
	}
	return responses
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
		Summary:       "Turn Off Two-Factor",
		Tags:          []string{tagTwoFactor},
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
