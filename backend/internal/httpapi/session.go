package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"

	"inbok/internal/account"
	"inbok/internal/session"
)

const (
	sessionsPath           = "/sessions"
	sessionCookieName      = "__Host-inbok-session"
	invalidCredentialsType = problemPrefix + "invalid-credentials"
	signInThrottledType    = problemPrefix + "sign-in-throttled"
)

// signInStep identifies the next step after password verification.
type signInStep string

const signInComplete signInStep = "complete"

// newSession is the sign-in request body. Password creation rules do not apply.
type newSession struct {
	Email    string `json:"email" format:"email" minLength:"3" maxLength:"254"`
	Password string `json:"password" writeOnly:"true" maxLength:"128"`
}

// sessionInput combines credentials with the resolved client address.
type sessionInput struct {
	Body newSession
	addr netip.Addr
}

// Resolve fills the client address for throttling using Gin's proxy rules.
// Huma calls it automatically after parsing the request, before the handler.
func (in *sessionInput) Resolve(ctx huma.Context) []error {
	return resolveClientAddr(ctx, &in.addr)
}

// signInResult tells the browser which sign-in step to display next.
type signInResult struct {
	Step signInStep `json:"step" enum:"complete"`
}

// sessionOutput is the sign-in response body and its session cookie.
type sessionOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      response[signInResult]
}

// sessionService is the session behavior called by these HTTP operations.
type sessionService interface {
	SignIn(ctx context.Context, email, password string, addr netip.Addr) (session.Issued, error)
}

// registerSession adds password sign-in and its documented failures.
func registerSession(api huma.API, service sessionService) {
	huma.Register(api, huma.Operation{
		OperationID:   "createSession",
		Method:        http.MethodPost,
		Path:          sessionsPath,
		Summary:       "Sign in with a password",
		DefaultStatus: http.StatusCreated,
		Responses:     problemResponses(api, http.StatusUnauthorized, http.StatusTooManyRequests),
	}, func(ctx context.Context, in *sessionInput) (*sessionOutput, error) {
		issued, err := service.SignIn(ctx, in.Body.Email, in.Body.Password, in.addr)
		if err != nil {
			return nil, signInProblem(err)
		}
		return &sessionOutput{
			SetCookie: http.Cookie{
				Name: sessionCookieName, Value: issued.Secret, Path: "/",
				Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
				MaxAge: int(time.Until(issued.State.ExpiresAt) / time.Second),
			},
			Body: respondData(signInResult{Step: signInComplete}).Body,
		}, nil
	})

	post := api.OpenAPI().Paths[sessionsPath].Post
	unauthorized := post.Responses[strconv.Itoa(http.StatusUnauthorized)]
	unauthorized.Description = "The email or password is incorrect."
	unauthorized.Content[problemContentType].Example = signInProblem(account.ErrInvalidCredentials)
	throttled := post.Responses[strconv.Itoa(http.StatusTooManyRequests)]
	throttled.Description = "Too many recent sign-in failures from this address."
	throttled.Content[problemContentType].Example = signInProblem(session.ErrThrottled)
}

// resolveClientAddr parses the address Gin selected for throttling.
func resolveClientAddr(ctx huma.Context, addr *netip.Addr) []error {
	parsed, err := netip.ParseAddr(humagin.Unwrap(ctx).ClientIP())
	if err != nil {
		return []error{huma.Error500InternalServerError("Cannot determine the client address.", err)}
	}
	*addr = parsed
	return nil
}

// signInProblem maps expected credential and throttle failures to safe text.
func signInProblem(err error) error {
	switch {
	case errors.Is(err, account.ErrInvalidCredentials):
		return newProblem(http.StatusUnauthorized, invalidCredentialsType, "Sign-in failed", "The email or password is incorrect.")
	case errors.Is(err, session.ErrThrottled):
		return newProblem(http.StatusTooManyRequests, signInThrottledType, "Too many attempts", "Too many failed attempts. Try again later.")
	default:
		return err
	}
}
