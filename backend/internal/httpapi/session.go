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
	"inbok/internal/totp"
)

const (
	sessionsPath                = "/sessions"
	currentSessionPath          = sessionsPath + "/current"
	sessionSecuritySchemeName   = "sessionCookie"
	sessionCookieName           = "__Host-inbok-session"
	challengeCookieName         = "__Host-inbok-challenge"
	challengeSecuritySchemeName = "challengeCookie"
)

// sessionService is the session behavior called by these HTTP operations.
type sessionService interface {
	StartSignIn(ctx context.Context, email, password string, addr netip.Addr) (session.SignedIn, error)
	Current(ctx context.Context, secret string) (session.Authenticated, error)
	SignOut(ctx context.Context, ref session.Reference) error
}

type codeSignInService interface {
	Complete(ctx context.Context, secret, code string) (session.Issued, error)
}

// sessionHandler holds the session operations' service dependencies.
type sessionHandler struct {
	service sessionService
	codes   codeSignInService
}

// registerSession adds sign-in, current account, and sign-out operations.
func registerSession(api huma.API, service sessionService, codes codeSignInService) {
	api.OpenAPI().Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		sessionSecuritySchemeName: {
			Type: "apiKey", In: "cookie", Name: sessionCookieName,
			Description: "Browser session cookie issued after complete sign-in. Browsers send it automatically on subsequent requests.",
		},
	}

	api.OpenAPI().Components.SecuritySchemes[challengeSecuritySchemeName] = &huma.SecurityScheme{
		Type: "apiKey", In: "cookie", Name: challengeCookieName,
		Description: "Temporary password proof. Grants no session access.",
	}

	handler := sessionHandler{service: service, codes: codes}
	registerSignIn(api, handler)
	registerCodeSignIn(api, handler)
	registerCurrentSession(api, handler)
	registerSignOut(api, handler)
}

// sessionCookie builds a host-only cookie for issuing or clearing a session.
func sessionCookie(secret string, maxAge int) http.Cookie {
	return http.Cookie{Name: sessionCookieName, Value: secret, Path: "/", Secure: true,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: maxAge}
}

// Public sign-in skips withAccess's no-store header.
// Run before input validation so failures receive it too.
func withNoStore(ctx huma.Context, next func(huma.Context)) {
	ctx.SetHeader("Cache-Control", "no-store")
	next(ctx)
}

func challengeCookie(secret string, maxAge int) http.Cookie {
	cookie := sessionCookie(secret, maxAge)
	cookie.Name = challengeCookieName
	return cookie
}

// ========================
// Sign-in
// ========================

const invalidCredentialsType = problemPrefix + "invalid-credentials"

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
	Step session.SignInStep `json:"step" enum:"complete,totp"`
}

// sessionOutput carries the next sign-in step and browser cookie changes.
type sessionOutput struct {
	SetCookie []http.Cookie `header:"Set-Cookie"`
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
		Middlewares:   huma.Middlewares{withNoStore},
		DefaultStatus: http.StatusCreated,
		Responses:     signInResponses(api),
	}, handler.handleCreateSession)
}

// handleCreateSession issues the cookie for the next sign-in step.
func (h sessionHandler) handleCreateSession(ctx context.Context, in *sessionInput) (*sessionOutput, error) {
	issued, err := h.service.StartSignIn(ctx, in.Body.Email, in.Body.Password, in.addr)
	if err != nil {
		return nil, signInProblem(err)
	}

	// The browser should keep only the proof cookie for the current step.
	maxAge := int(time.Until(issued.ExpiresAt) / time.Second)
	var cookies []http.Cookie

	switch issued.Step {
	case session.SignInStepComplete:
		cookies = []http.Cookie{sessionCookie(issued.Secret, maxAge), challengeCookie("", -1)}
	case session.SignInStepTOTP:
		cookies = []http.Cookie{challengeCookie(issued.Secret, maxAge), sessionCookie("", -1)}
	default:
		return nil, errors.New("invalid sign-in result step")
	}

	return &sessionOutput{
		SetCookie: cookies,
		Body:      respondData(signInResult{Step: issued.Step}).Body,
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
// Code sign-in
// ========================

const (
	invalidChallengeType  = problemPrefix + "sign-in-challenge-invalid"
	invalidSignInCodeType = problemPrefix + "invalid-sign-in-code"
)

type newCodeSession struct {
	Code string `json:"code" writeOnly:"true" maxLength:"128" doc:"Authenticator code or unused backup code."`
}

type codeSessionInput struct {
	challenge string
	Body      newCodeSession
}

// Resolve reads the HttpOnly proof cookie without making it a client argument.
func (in *codeSessionInput) Resolve(ctx huma.Context) []error {
	cookie, err := huma.ReadCookie(ctx, challengeCookieName)
	if err == nil {
		in.challenge = cookie.Value
	}
	return nil
}

func registerCodeSignIn(api huma.API, handler sessionHandler) {
	huma.Register(api, huma.Operation{
		OperationID:   "createSessionWithCode",
		Method:        http.MethodPost,
		Path:          sessionsPath + "/totp",
		Summary:       "Complete sign-in with an authenticator or backup code",
		DefaultStatus: http.StatusCreated,
		Metadata:      map[string]any{accessKey: session.AccessPublic},
		Security:      []map[string][]string{{challengeSecuritySchemeName: {}}},
		Middlewares:   huma.Middlewares{withNoStore},
		Responses:     codeSignInResponses(api),
	}, handler.handleCreateSessionWithCode)
}

func (h sessionHandler) handleCreateSessionWithCode(ctx context.Context, in *codeSessionInput) (*sessionOutput, error) {
	issued, err := h.codes.Complete(ctx, in.challenge, in.Body.Code)
	if err != nil {
		problem := codeSignInProblem(err)
		if errors.Is(err, session.ErrInvalidChallenge) {
			// Discard unusable proof so the browser can start with a password again.
			cleared := challengeCookie("", -1)
			return nil, huma.ErrorWithHeaders(problem, http.Header{"Set-Cookie": {cleared.String()}})
		}
		return nil, problem
	}

	// Replace password proof with the committed session.
	return &sessionOutput{
		SetCookie: []http.Cookie{
			sessionCookie(issued.Secret, int(time.Until(issued.State.ExpiresAt)/time.Second)),
			challengeCookie("", -1),
		},
		Body: respondData(signInResult{Step: session.SignInStepComplete}).Body,
	}, nil
}

func codeSignInResponses(api huma.API) map[string]*huma.Response {
	responses := problemResponses(api, http.StatusUnauthorized, http.StatusUnprocessableEntity, http.StatusTooManyRequests)

	unauthorized := responses[strconv.Itoa(http.StatusUnauthorized)]
	unauthorized.Description = "The challenge is missing, expired, consumed, or exhausted. Enter the password again."
	unauthorized.Content[problemContentType].Example = codeSignInProblem(session.ErrInvalidChallenge)

	invalid := responses[strconv.Itoa(http.StatusUnprocessableEntity)]
	invalid.Description = "The request inputs or second-factor code are invalid."
	invalid.Content[problemContentType].Example = codeSignInProblem(totp.ErrInvalidCode)

	throttled := responses[strconv.Itoa(http.StatusTooManyRequests)]
	throttled.Description = "Too many recent failed code attempts for this account."
	throttled.Content[problemContentType].Example = totpThrottleProblem(totp.ErrThrottled)

	return responses
}

func codeSignInProblem(err error) error {
	switch {
	case errors.Is(err, session.ErrInvalidChallenge):
		return newProblem(
			http.StatusUnauthorized, invalidChallengeType,
			"Sign in again", "Enter your email and password to start sign-in again.",
		)
	case errors.Is(err, totp.ErrInvalidCode):
		return newProblem(
			http.StatusUnprocessableEntity, invalidSignInCodeType,
			"Invalid sign-in code", "Enter a current authenticator code or an unused backup code.",
			inputError{
				Location: "body.code",
				Message:  "Enter a current authenticator code or an unused backup code.",
			},
		)
	default:
		return totpThrottleProblem(err)
	}
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
