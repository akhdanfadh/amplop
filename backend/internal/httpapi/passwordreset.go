package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"inbok/internal/account"
	"inbok/internal/emailaddress"
	"inbok/internal/passwordreset"
	"inbok/internal/session"
)

const (
	passwordResetPath       = "/password-reset"
	passwordResetLinksPath  = passwordResetPath + "/links"
	passwordResetRedeemPath = passwordResetLinksPath + "/redeem"
)

type passwordResetRequestService interface {
	Request(ctx context.Context, email string, client netip.Addr) error
}

type passwordResetCompletionService interface {
	Complete(ctx context.Context, secret, email, password string) (account.User, error)
}

type passwordResetHandler struct {
	requests   passwordResetRequestService
	completion passwordResetCompletionService
}

func registerPasswordReset(api huma.API, requests passwordResetRequestService, completion passwordResetCompletionService) {
	handler := passwordResetHandler{requests: requests, completion: completion}

	registerPasswordResetRequest(api, handler)
	registerPasswordResetComplete(api, handler)
}

// ========================
// Password reset link request
// ========================

const passwordResetThrottledType = problemPrefix + "password-reset-throttled"

// passwordResetRequestStatus is the one answer every accepted request gets.
type passwordResetRequestStatus string

const passwordResetRequested passwordResetRequestStatus = "requested"

// newPasswordResetRequest is the request body.
type newPasswordResetRequest struct {
	Email string `json:"email" format:"email" minLength:"3" maxLength:"254" doc:"The email the account signs in with. Any valid email gets the same answer."`
}

// passwordResetRequestInput combines the email with the resolved client
// address.
type passwordResetRequestInput struct {
	Body newPasswordResetRequest
	addr netip.Addr
}

// Resolve fills the client address for throttling using Gin's proxy rules.
// Huma calls it automatically after parsing the request, before the handler.
func (in *passwordResetRequestInput) Resolve(ctx huma.Context) []error {
	return resolveClientAddr(ctx, &in.addr)
}

type passwordResetRequestResult struct {
	Status passwordResetRequestStatus `json:"status" enum:"requested" doc:"The same for every accepted request, so it does not tell whether the email has an account."`
}

func registerPasswordResetRequest(api huma.API, handler passwordResetHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "requestPasswordResetLink",
		Method:      http.MethodPost,
		Path:        passwordResetLinksPath,
		Summary:     "Send Password Reset Link",
		Tags:        []string{tagPasswordReset},
		Description: "Answers at once, the same for every email. Afterwards, an active account with that verified email gets a link that expires in 1 hour, " +
			"unless 3 links were sent to it in the last hour or 5 in 24 hours; any other email gets nothing. " +
			"Earlier links keep working. One client may ask 10 times in 15 minutes.",
		Metadata:      map[string]any{accessKey: session.AccessPublic},
		Middlewares:   huma.Middlewares{withNoStore},
		DefaultStatus: http.StatusAccepted,
		Responses:     passwordResetRequestResponses(api),
	}, handler.handleRequestPasswordResetLink)
}

func (h passwordResetHandler) handleRequestPasswordResetLink(ctx context.Context, in *passwordResetRequestInput) (*output[passwordResetRequestResult], error) {
	if err := h.requests.Request(ctx, in.Body.Email, in.addr); err != nil {
		return nil, passwordResetRequestProblem(err)
	}
	return respondData(passwordResetRequestResult{Status: passwordResetRequested}), nil
}

func passwordResetRequestResponses(api huma.API) map[string]*huma.Response {
	responses := problemResponses(api, http.StatusUnprocessableEntity, http.StatusTooManyRequests)

	invalid := responses[strconv.Itoa(http.StatusUnprocessableEntity)]
	invalid.Description = "The email is not valid."
	invalid.Content[problemContentType].Example = passwordResetRequestProblem(emailaddress.ErrInvalid)

	throttled := responses[strconv.Itoa(http.StatusTooManyRequests)]
	throttled.Description = "This client asked for too many reset links recently."
	throttled.Content[problemContentType].Example = passwordResetRequestProblem(passwordreset.ErrThrottled)
	return responses
}

func passwordResetRequestProblem(err error) error {
	switch {
	case errors.Is(err, emailaddress.ErrInvalid):
		return invalidEmailProblem("body.email")
	case errors.Is(err, passwordreset.ErrThrottled):
		return retryAfterProblem(newProblem(http.StatusTooManyRequests, passwordResetThrottledType, "Too many password reset requests", "Too many password reset requests were made recently."), err)
	}
	return err
}

// ========================
// Password reset completion
// ========================

const passwordResetLinkInvalidType = problemPrefix + "password-reset-link-invalid"

// newPasswordFromLink is the completion request body.
type newPasswordFromLink struct {
	Secret   string `json:"secret" writeOnly:"true" doc:"The text after # in the emailed link."`
	Email    string `json:"email" format:"email" minLength:"3" maxLength:"254" doc:"The email the link was sent to, typed by the person using it. Letters A to Z match in either case."`
	Password string `json:"password" writeOnly:"true" minLength:"15" maxLength:"128"`
}

type passwordResetResult struct {
	Email string `json:"email" doc:"The account's email, for signing in with the new password."`
}

func registerPasswordResetComplete(api huma.API, handler passwordResetHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "redeemPasswordResetLink",
		Method:      http.MethodPost,
		Path:        passwordResetRedeemPath,
		Summary:     "Reset Password",
		Tags:        []string{tagPasswordReset},
		Description: "Sets the password of the account the link was sent to, when the email given is the one the link was sent to; " +
			"signs that account out everywhere and uses up all of its reset links. " +
			"TOTP stays on. Needs no session and signs no one in; the account's owner is emailed that the password changed.",
		Metadata:    map[string]any{accessKey: session.AccessPublic},
		Middlewares: huma.Middlewares{withNoStore},
		Responses:   passwordResetCompleteResponses(api),
	}, handler.handleRedeemPasswordResetLink)
}

func (h passwordResetHandler) handleRedeemPasswordResetLink(ctx context.Context, in *struct{ Body newPasswordFromLink }) (*output[passwordResetResult], error) {
	user, err := h.completion.Complete(ctx, in.Body.Secret, in.Body.Email, in.Body.Password)
	if err != nil {
		return nil, passwordResetCompleteProblem(err)
	}
	return respondData(passwordResetResult{Email: user.Email}), nil
}

func passwordResetCompleteResponses(api huma.API) map[string]*huma.Response {
	responses := problemResponses(api, http.StatusNotFound, http.StatusUnprocessableEntity)

	notFound := responses[strconv.Itoa(http.StatusNotFound)]
	notFound.Description = "The link is unknown, used, or expired, or its account is deactivated or no longer has the email it was sent to."
	notFound.Content[problemContentType].Example = passwordResetCompleteProblem(passwordreset.ErrLinkInvalid)

	invalid := responses[strconv.Itoa(http.StatusUnprocessableEntity)]
	invalid.Description = "The request inputs are invalid, or the link was sent to a different email."
	invalid.Content[problemContentType].Examples = map[string]*huma.Example{
		"password":      {Value: passwordResetCompleteProblem(account.ErrPasswordInvalid)},
		"emailMismatch": {Value: passwordResetCompleteProblem(passwordreset.ErrEmailMismatch)},
	}
	return responses
}

func passwordResetCompleteProblem(err error) error {
	switch {
	// Huma's length check refuses almost every such password first; the
	// service's rule decides the rest.
	case errors.Is(err, account.ErrPasswordInvalid):
		return invalidPasswordProblem("body.password")
	case errors.Is(err, emailaddress.ErrInvalid):
		return invalidEmailProblem("body.email")
	case errors.Is(err, passwordreset.ErrEmailMismatch):
		return linkEmailMismatchProblem()
	case errors.Is(err, passwordreset.ErrLinkInvalid):
		// One answer for every reason, so an old link tells nothing about
		// the account.
		return newProblem(http.StatusNotFound, passwordResetLinkInvalidType, "Link not valid", "This link has expired, was already used, or is not valid. Ask for a new one.")
	}
	return err
}
