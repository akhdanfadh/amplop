package httpapi

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"inbok/internal/account"
	"inbok/internal/emailverification"
	"inbok/internal/session"
	"inbok/internal/systememail"
)

type emailVerificationService interface {
	Request(ctx context.Context, userID int64) (string, error)
	Redeem(ctx context.Context, secret string) error
}

type emailVerificationHandler struct {
	service emailVerificationService
}

func registerEmailVerification(api huma.API, service emailVerificationService) {
	handler := emailVerificationHandler{service: service}

	registerEmailVerificationRequest(api, handler)
	registerEmailVerificationRedeem(api, handler)
}

// ========================
// Email verification request
// ========================

const (
	emailAlreadyVerifiedType       = problemPrefix + "email-already-verified"
	emailVerificationThrottledType = problemPrefix + "email-verification-throttled"
	emailNotSentType               = problemPrefix + "email-not-sent"
)

type sentEmailVerification struct {
	Address string `json:"address" doc:"Where the link was sent: the account's email address."`
}

func registerEmailVerificationRequest(api huma.API, handler emailVerificationHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "requestEmailVerification",
		Method:      http.MethodPost,
		Path:        "/account/email-verification",
		Summary:     "Email a link that verifies the account's email address",
		Description: "The link expires in 24 hours, and every unexpired link works until one is used. " +
			"At most 3 links are sent in 15 minutes and 10 in 24 hours.",
		Metadata:  map[string]any{accessKey: session.AccessMember},
		Security:  []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses: emailVerificationRequestResponses(api),
	}, handler.handleRequestEmailVerification)
}

func (h emailVerificationHandler) handleRequestEmailVerification(ctx context.Context, _ *struct{}) (*output[sentEmailVerification], error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	address, err := h.service.Request(ctx, user.ID)
	if err != nil {
		return nil, emailVerificationRequestProblem(err)
	}
	return respondData(sentEmailVerification{Address: address}), nil
}

func emailVerificationRequestResponses(api huma.API) map[string]*huma.Response {
	responses := totpStatusResponses(api)
	maps.Copy(responses, problemResponses(api, http.StatusConflict, http.StatusTooManyRequests, http.StatusBadGateway))

	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "The address is already verified, or no system email settings are saved."
	conflict.Content[problemContentType].Examples = map[string]*huma.Example{
		"alreadyVerified": {Value: emailVerificationRequestProblem(emailverification.ErrAlreadyVerified)},
		"notConfigured":   {Value: emailVerificationRequestProblem(systememail.ErrNotConfigured)},
	}

	throttled := responses[strconv.Itoa(http.StatusTooManyRequests)]
	throttled.Description = "Too many links were sent recently."
	throttled.Content[problemContentType].Example = emailVerificationRequestProblem(emailverification.ErrThrottled)

	badGateway := responses[strconv.Itoa(http.StatusBadGateway)]
	badGateway.Description = "Sending failed or timed out. The link was saved and counts toward the limit; after a timeout the message may still arrive."
	badGateway.Content[problemContentType].Example = emailVerificationRequestProblem(&systememail.SendError{})
	return responses
}

func emailVerificationRequestProblem(err error) error {
	switch {
	// The account was deleted or deactivated after its session was checked.
	case errors.Is(err, account.ErrUserNotFound):
		return unauthenticatedProblem()
	case errors.Is(err, emailverification.ErrAlreadyVerified):
		return newProblem(http.StatusConflict, emailAlreadyVerifiedType, "Email already verified", "Your email address is already verified.")
	case errors.Is(err, systememail.ErrNotConfigured):
		return newProblem(http.StatusConflict, systemEmailNotConfiguredType, "System email not configured", "Email can't be sent until an administrator sets up system email.")
	case errors.Is(err, emailverification.ErrThrottled):
		return retryAfterProblem(newProblem(http.StatusTooManyRequests, emailVerificationThrottledType, "Too many links", "Too many verification links were sent recently."), err)
	}
	// The server's reason stays in the log; it can name the username.
	if _, ok := errors.AsType[*systememail.SendError](err); ok {
		return newProblem(http.StatusBadGateway, emailNotSentType, "Email not sent", "The email could not be sent. Try again later.")
	}
	return err
}

// ========================
// Email verification redeem
// ========================

const emailVerificationLinkInvalidType = problemPrefix + "email-verification-link-invalid"

type emailVerificationSecret struct {
	Secret string `json:"secret" writeOnly:"true" doc:"The text after # in the emailed link."`
}

// emailVerificationStatus is the result of redeeming a link.
type emailVerificationStatus string

const emailVerificationVerified emailVerificationStatus = "verified"

type emailVerificationResult struct {
	Status emailVerificationStatus `json:"status" enum:"verified"`
}

func registerEmailVerificationRedeem(api huma.API, handler emailVerificationHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "redeemEmailVerification",
		Method:      http.MethodPost,
		Path:        "/email-verifications/redeem",
		Summary:     "Verify an email address with an emailed link",
		Description: "Needs no session and signs no one in. Redeeming a link uses up every link sent to that account.",
		Metadata:    map[string]any{accessKey: session.AccessPublic},
		Middlewares: huma.Middlewares{withNoStore},
		Responses:   emailVerificationRedeemResponses(api),
	}, handler.handleRedeemEmailVerification)
}

func (h emailVerificationHandler) handleRedeemEmailVerification(ctx context.Context, in *struct{ Body emailVerificationSecret }) (*output[emailVerificationResult], error) {
	if err := h.service.Redeem(ctx, in.Body.Secret); err != nil {
		return nil, emailVerificationRedeemProblem(err)
	}
	return respondData(emailVerificationResult{Status: emailVerificationVerified}), nil
}

func emailVerificationRedeemResponses(api huma.API) map[string]*huma.Response {
	responses := problemResponses(api, http.StatusNotFound, http.StatusUnprocessableEntity)

	notFound := responses[strconv.Itoa(http.StatusNotFound)]
	notFound.Description = "The link is unknown, used, expired, or was sent to an address the account no longer has."
	notFound.Content[problemContentType].Example = emailVerificationRedeemProblem(emailverification.ErrLinkInvalid)

	invalid := responses[strconv.Itoa(http.StatusUnprocessableEntity)]
	invalid.Description = "The request inputs are invalid."
	invalid.Content[problemContentType].Example = requestProblem(http.StatusUnprocessableEntity)
	return responses
}

func emailVerificationRedeemProblem(err error) error {
	if errors.Is(err, emailverification.ErrLinkInvalid) {
		return newProblem(http.StatusNotFound, emailVerificationLinkInvalidType, "Link not valid", "This link has expired, was already used, or is not valid. Ask for a new one.")
	}
	return err
}
