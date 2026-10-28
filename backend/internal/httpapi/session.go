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
	"inbok/internal/session"
)

const (
	sessionsPath              = "/sessions"
	currentSessionPath        = sessionsPath + "/current"
	sessionSecuritySchemeName = "sessionCookie"
	sessionCookieName         = "__Host-inbok-session"
)

// sessionService is the session behavior called by these HTTP operations.
type sessionService interface {
	SignIn(ctx context.Context, email, password string, addr netip.Addr) (session.Issued, error)
	Current(ctx context.Context, secret string) (session.Authenticated, error)
	SignOut(ctx context.Context, ref session.Reference) error
}

// sessionHandler holds the session operations' service dependency.
type sessionHandler struct {
	service sessionService
}

// registerSession adds sign-in, current account, and sign-out operations.
func registerSession(api huma.API, service sessionService) {
	api.OpenAPI().Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		sessionSecuritySchemeName: {
			Type: "apiKey", In: "cookie", Name: sessionCookieName,
			Description: "Browser session cookie issued by POST /sessions. Browsers send it automatically on subsequent requests.",
		},
	}

	handler := sessionHandler{service: service}
	registerSignIn(api, handler)
	registerCurrentSession(api, handler)
	registerSignOut(api, handler)
}

// sessionCookie builds a host-only cookie for issuing or clearing a session.
func sessionCookie(secret string, maxAge int) http.Cookie {
	return http.Cookie{Name: sessionCookieName, Value: secret, Path: "/", Secure: true,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: maxAge}
}

// ========================
// Sign-in
// ========================

const invalidCredentialsType = problemPrefix + "invalid-credentials"

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

// registerSignIn adds password sign-in.
func registerSignIn(api huma.API, handler sessionHandler) {
	huma.Register(api, huma.Operation{
		OperationID:   "createSession",
		Metadata:      map[string]any{accessKey: session.AccessPublic},
		Method:        http.MethodPost,
		Path:          sessionsPath,
		Summary:       "Sign in with a password",
		DefaultStatus: http.StatusCreated,
		Responses:     signInResponses(api),
	}, handler.handleCreateSession)
}

// handleCreateSession issues a browser session after checking credentials.
func (h sessionHandler) handleCreateSession(ctx context.Context, in *sessionInput) (*sessionOutput, error) {
	issued, err := h.service.SignIn(ctx, in.Body.Email, in.Body.Password, in.addr)
	if err != nil {
		return nil, signInProblem(err)
	}
	return &sessionOutput{
		SetCookie: sessionCookie(issued.Secret, int(time.Until(issued.State.ExpiresAt)/time.Second)),
		Body:      respondData(signInResult{Step: signInComplete}).Body,
	}, nil
}

// signInResponses documents credential and throttle failures.
func signInResponses(api huma.API) map[string]*huma.Response {
	responses := problemResponses(api, http.StatusUnauthorized, http.StatusTooManyRequests)

	unauthorized := responses[strconv.Itoa(http.StatusUnauthorized)]
	unauthorized.Description = "The email or password is incorrect."
	unauthorized.Content[problemContentType].Example = signInProblem(account.ErrInvalidCredentials)

	throttled := responses[strconv.Itoa(http.StatusTooManyRequests)]
	throttled.Description = "Too many recent sign-in failures from this address."
	throttled.Content[problemContentType].Example = throttleProblem(session.ErrThrottled)

	return responses
}

// signInProblem maps expected credential and throttle failures to safe text.
func signInProblem(err error) error {
	if errors.Is(err, account.ErrInvalidCredentials) {
		return newProblem(http.StatusUnauthorized, invalidCredentialsType, "Sign-in failed", "The email or password is incorrect.")
	}

	return throttleProblem(err)
}

// ========================
// Current session
// ========================

// currentSession is the signed-in account and its TOTP enrollment requirement.
type currentSession struct {
	Email        string       `json:"email"`
	Role         account.Role `json:"role" enum:"member,admin"`
	TOTPRequired bool         `json:"totpRequired"`
}

// registerCurrentSession adds the signed-in account operation.
func registerCurrentSession(api huma.API, handler sessionHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "getCurrentSession",
		Method:      http.MethodGet,
		Path:        currentSessionPath,
		Summary:     "Get the signed-in account",
		Metadata:    map[string]any{accessKey: session.AccessMember},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   currentSessionResponses(api),
	}, handler.handleGetCurrentSession)
}

// handleGetCurrentSession returns the identity checked by access middleware.
func (h sessionHandler) handleGetCurrentSession(ctx context.Context, _ *struct{}) (*output[currentSession], error) {
	identity := ctx.Value(sessionKey{}).(session.Authenticated).Identity
	return respondData(currentSession{Email: identity.User.Email, Role: identity.User.Role, TOTPRequired: identity.TOTPRequired}), nil
}

func currentSessionResponses(api huma.API) map[string]*huma.Response {
	responses := problemResponses(api, http.StatusUnauthorized)
	unauthenticated := responses[strconv.Itoa(http.StatusUnauthorized)]
	unauthenticated.Description = "No valid session identifies a current account."
	unauthenticated.Content[problemContentType].Example = unauthenticatedProblem()

	return responses
}

// ========================
// Sign-out
// ========================

// signOutOutput clears the browser cookie without a response body.
type signOutOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
}

// registerSignOut adds browser session deletion.
func registerSignOut(api huma.API, handler sessionHandler) {
	huma.Register(api, huma.Operation{
		OperationID:   "deleteCurrentSession",
		Method:        http.MethodDelete,
		Path:          currentSessionPath,
		Summary:       "Sign out of the current session",
		DefaultStatus: http.StatusNoContent,
		Metadata:      map[string]any{accessKey: session.AccessMember},
		Security:      []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:     signOutResponses(api),
	}, handler.handleDeleteCurrentSession)
}

// handleDeleteCurrentSession deletes the checked session and clears its cookie.
func (h sessionHandler) handleDeleteCurrentSession(ctx context.Context, _ *struct{}) (*signOutOutput, error) {
	current := ctx.Value(sessionKey{}).(session.Authenticated)
	if err := h.service.SignOut(ctx, current.Session); err != nil {
		return nil, err
	}
	return &signOutOutput{SetCookie: sessionCookie("", -1)}, nil
}

func signOutResponses(api huma.API) map[string]*huma.Response {
	responses := problemResponses(api, http.StatusUnauthorized)
	unauthenticated := responses[strconv.Itoa(http.StatusUnauthorized)]
	unauthenticated.Description = "No valid session identifies a current account. The session cookie is cleared."
	unauthenticated.Content[problemContentType].Example = unauthenticatedProblem()

	return responses
}
