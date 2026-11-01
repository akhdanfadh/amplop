package httpapi

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"strconv"
	"time"
	"uuid"

	"github.com/danielgtaylor/huma/v2"

	"inbok/internal/account"
	"inbok/internal/session"
	"inbok/internal/totp"
)

const (
	sessionsPath                = "/sessions"
	currentSessionPath          = sessionsPath + "/current"
	otherSessionsPath           = sessionsPath + "/others"
	sessionSecuritySchemeName   = "sessionCookie"
	sessionCookieName           = "__Host-inbok-session"
	challengeCookieName         = "__Host-inbok-challenge"
	challengeSecuritySchemeName = "challengeCookie"
)

// sessionService is the session behavior called by these HTTP operations.
type sessionService interface {
	StartSignIn(ctx context.Context, email, password string, device session.Device) (session.SignedIn, error)
	Current(ctx context.Context, secret string) (session.Authenticated, error)
	SignOut(ctx context.Context, ref session.Reference) error
	List(ctx context.Context, current session.Authenticated) ([]session.Listed, error)
	End(ctx context.Context, current session.Authenticated, id uuid.UUID) error
	EndOthers(ctx context.Context, current session.Authenticated) (int, error)
}

type codeSignInService interface {
	Complete(ctx context.Context, secret, code string, device session.Device) (session.Issued, error)
}

// sessionHandler holds the session operations' service dependencies.
type sessionHandler struct {
	service sessionService
	codes   codeSignInService
}

// registerSession adds sign-in, current account, sign-out, and the operations
// that list and end the user's sessions.
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
	registerSessionList(api, handler)
	registerSessionEnd(api, handler)
	registerOtherSessionsEnd(api, handler)
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

// sessionInput combines credentials with the requesting browser's details.
type sessionInput struct {
	Body   newSession
	device session.Device
}

// Resolve reads the device details: the User-Agent and the client address
// Gin's proxy rules select, used for throttling and kept by a new session.
// Huma calls it automatically after parsing the request, before the handler.
func (in *sessionInput) Resolve(ctx huma.Context) []error {
	return resolveDevice(ctx, &in.device)
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
	issued, err := h.service.StartSignIn(ctx, in.Body.Email, in.Body.Password, in.device)
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
	device    session.Device
	Body      newCodeSession
}

// Resolve reads the HttpOnly proof cookie without making it a client argument.
// The new session keeps this request's device details, not the password step's.
func (in *codeSessionInput) Resolve(ctx huma.Context) []error {
	cookie, err := huma.ReadCookie(ctx, challengeCookieName)
	if err == nil {
		in.challenge = cookie.Value
	}
	return resolveDevice(ctx, &in.device)
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
	issued, err := h.codes.Complete(ctx, in.challenge, in.Body.Code, in.device)
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

// ========================
// Session list
// ========================

// listedSession is one of the user's live sessions. Browser and OS are empty
// when the User-Agent does not name them.
type listedSession struct {
	ID           uuid.UUID `json:"id" format:"uuid"`
	Browser      string    `json:"browser"`
	OS           string    `json:"os"`
	SignedInAt   time.Time `json:"signedInAt"`
	LastActiveAt time.Time `json:"lastActiveAt" doc:"Updated at most once an hour."`
	SignedInFrom string    `json:"signedInFrom" doc:"Client address at sign-in. Empty when unknown."`
	Current      bool      `json:"current" doc:"True for the session that made this request."`
}

type listedSessions struct {
	Sessions []listedSession `json:"sessions" nullable:"false" doc:"Live sessions, newest sign-in first."`
}

func registerSessionList(api huma.API, handler sessionHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "listSessions",
		Method:      http.MethodGet,
		Path:        sessionsPath,
		Summary:     "List the signed-in account's sessions",
		Metadata:    map[string]any{accessKey: session.AccessMember},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   currentSessionResponses(api),
	}, handler.handleListSessions)
}

func (h sessionHandler) handleListSessions(ctx context.Context, _ *struct{}) (*output[listedSessions], error) {
	current := ctx.Value(sessionKey{}).(session.Authenticated)
	sessions, err := h.service.List(ctx, current)
	if errors.Is(err, session.ErrUnauthenticated) {
		return nil, unauthenticatedProblem()
	}
	if err != nil {
		return nil, err
	}
	listed := listedSessions{Sessions: make([]listedSession, 0, len(sessions))}
	for _, l := range sessions {
		item := listedSession{
			ID: l.ID, Browser: l.Browser, OS: l.OS,
			SignedInAt: l.CreatedAt, LastActiveAt: l.LastUsedAt, Current: l.Current,
		}
		if l.SignedInFrom.IsValid() {
			item.SignedInFrom = l.SignedInFrom.String()
		}
		listed.Sessions = append(listed.Sessions, item)
	}
	return respondData(listed), nil
}

// ========================
// Session end
// ========================

const (
	sessionNotFoundType  = problemPrefix + "session-not-found"
	sessionIsCurrentType = problemPrefix + "session-is-current"
)

// endedSessions counts the sessions a request ended.
type endedSessions struct {
	Ended int `json:"ended" minimum:"0"`
}

type sessionEndInput struct {
	ID uuid.UUID `path:"id" format:"uuid" doc:"Session ID from the session list."`
}

func registerSessionEnd(api huma.API, handler sessionHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "deleteSession",
		Method:      http.MethodDelete,
		Path:        sessionsPath + "/{id}",
		Summary:     "End another session of the signed-in account",
		Description: "To end the current session, sign out instead.",
		Metadata:    map[string]any{accessKey: session.AccessMember},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   sessionEndResponses(api),
	}, handler.handleDeleteSession)
}

func (h sessionHandler) handleDeleteSession(ctx context.Context, in *sessionEndInput) (*output[endedSessions], error) {
	current := ctx.Value(sessionKey{}).(session.Authenticated)
	if err := h.service.End(ctx, current, in.ID); err != nil {
		return nil, sessionEndProblem(err)
	}
	return respondData(endedSessions{Ended: 1}), nil
}

func sessionEndResponses(api huma.API) map[string]*huma.Response {
	responses := currentSessionResponses(api)
	maps.Copy(responses, problemResponses(api, http.StatusNotFound, http.StatusConflict))

	notFound := responses[strconv.Itoa(http.StatusNotFound)]
	notFound.Description = "The session has ended, expired, or is not one of this account's."
	notFound.Content[problemContentType].Example = sessionEndProblem(session.ErrNotFound)

	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "The session is the one making this request. Sign out instead."
	conflict.Content[problemContentType].Example = sessionEndProblem(session.ErrCurrentSession)

	return responses
}

// sessionEndProblem maps the errors of ending one session.
func sessionEndProblem(err error) error {
	switch {
	case errors.Is(err, session.ErrUnauthenticated):
		return unauthenticatedProblem()
	case errors.Is(err, session.ErrNotFound):
		return newProblem(http.StatusNotFound, sessionNotFoundType, "Session not found", "The session has already ended or does not exist.")
	case errors.Is(err, session.ErrCurrentSession):
		return newProblem(http.StatusConflict, sessionIsCurrentType, "Session is current", "Sign out to end this browser's session.")
	default:
		return err
	}
}

// ========================
// Other sessions end
// ========================

func registerOtherSessionsEnd(api huma.API, handler sessionHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "deleteOtherSessions",
		Method:      http.MethodDelete,
		Path:        otherSessionsPath,
		Summary:     "End every other session of the signed-in account",
		Description: "Keeps the current session. The count excludes sessions that had already expired.",
		Metadata:    map[string]any{accessKey: session.AccessMember},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   currentSessionResponses(api),
	}, handler.handleDeleteOtherSessions)
}

func (h sessionHandler) handleDeleteOtherSessions(ctx context.Context, _ *struct{}) (*output[endedSessions], error) {
	current := ctx.Value(sessionKey{}).(session.Authenticated)
	ended, err := h.service.EndOthers(ctx, current)
	if errors.Is(err, session.ErrUnauthenticated) {
		return nil, unauthenticatedProblem()
	}
	if err != nil {
		return nil, err
	}
	return respondData(endedSessions{Ended: ended}), nil
}
