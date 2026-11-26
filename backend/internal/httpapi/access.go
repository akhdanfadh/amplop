package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"

	"amplop/internal/account"
	"amplop/internal/session"
)

// accessKey names the shared operation metadata entry for required access.
const accessKey = "access"

// sessionKey identifies the checked session and identity in the request context.
type sessionKey struct{}

// withAccess enforces each operation's access level before parsing its inputs.
// Missing or invalid access metadata requires admin access.
func withAccess(api huma.API, service sessionService) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		level := operationAccess(ctx.Operation())
		if level == session.AccessPublic {
			next(ctx)
			return
		}

		// Keep account-specific responses out of browser and proxy HTTP caches.
		ctx.SetHeader("Cache-Control", "no-store")
		cookie, cookieErr := huma.ReadCookie(ctx, sessionCookieName)
		var current session.Authenticated
		err := session.ErrUnauthenticated
		if cookieErr == nil && cookie.Value != "" {
			current, err = service.Current(ctx.Context(), cookie.Value)
		}
		if errors.Is(err, session.ErrUnauthenticated) {
			cleared := sessionCookie("", -1)
			ctx.AppendHeader("Set-Cookie", cleared.String())
			writeProblem(humagin.Unwrap(ctx), unauthenticatedProblem())
			return
		}
		if err != nil {
			huma.WriteErr(api, ctx, http.StatusInternalServerError, "Session check failed.", err)
			return
		}

		identity := current.Identity
		known := identity.Access >= session.AccessEnrollment && identity.Access <= session.AccessAdmin && knownRole(identity)
		// An administrator without two-factor authentication may only set it up.
		if known && level > session.AccessEnrollment && identity.User.Role == account.RoleAdmin && identity.TOTPRequired {
			writeProblem(humagin.Unwrap(ctx), totpRequiredProblem())
			return
		}
		if !known || identity.Access < level || (level == session.AccessAdmin && identity.User.Role != account.RoleAdmin) {
			writeProblem(humagin.Unwrap(ctx), forbiddenProblem())
			return
		}
		next(huma.WithValue(ctx, sessionKey{}, current))
	}
}

// operationAccess returns op's required access level from its metadata.
// Missing or invalid metadata requires admin access.
func operationAccess(op *huma.Operation) session.Access {
	level, ok := op.Metadata[accessKey].(session.Access)
	if !ok || level < session.AccessPublic || level > session.AccessAdmin {
		return session.AccessAdmin
	}
	return level
}

// memberAllowed reports whether identity may use member operations.
func memberAllowed(identity session.Identity) bool {
	return identity.Access >= session.AccessMember && identity.Access <= session.AccessAdmin && knownRole(identity)
}

// knownRole reports whether identity's role is one access checks know.
func knownRole(identity session.Identity) bool {
	return identity.User.Role == account.RoleMember || identity.User.Role == account.RoleAdmin
}

const unauthenticatedType = problemPrefix + "unauthenticated"

// unauthenticatedDescription documents every 401 unauthenticated response.
const unauthenticatedDescription = "No valid session identifies a current account. The session cookie is cleared."

// unauthenticatedProblem is the safe response for a missing or invalid
// session.
//
// DESIGN: every response with it clears the session cookie. [withAccess] sets
// the header itself, and [normalizeError] sets it on a handler's response, so
// a new handler gets it without asking. The alternative, a helper each
// handler returns instead, makes a handler that forgets it leave a dead
// cookie behind.
func unauthenticatedProblem() *problem {
	return newProblem(http.StatusUnauthorized, unauthenticatedType, "Sign-in required", "Sign in to continue.")
}

// totpRequiredProblem is the response for an administrator without
// confirmed TOTP, whose session may only set it up.
func totpRequiredProblem() *problem {
	return newProblem(http.StatusForbidden, problemPrefix+"totp-required",
		"TOTP required", "Set up two-factor authentication to continue.")
}

// forbiddenProblem is the response for an account without the required access.
func forbiddenProblem() *problem {
	return newProblem(http.StatusForbidden, problemPrefix+"forbidden",
		"Access denied", "You do not have access to this operation.")
}

// adminResponses documents the failures every administrator operation shares.
func adminResponses(api huma.API, statuses ...int) map[string]*huma.Response {
	responses := problemResponses(api, append([]int{http.StatusUnauthorized, http.StatusForbidden}, statuses...)...)

	unauthorized := responses[strconv.Itoa(http.StatusUnauthorized)]
	unauthorized.Description = unauthenticatedDescription
	unauthorized.Content[problemContentType].Example = unauthenticatedProblem()

	forbidden := responses[strconv.Itoa(http.StatusForbidden)]
	forbidden.Description = "The account is not an administrator."
	forbidden.Content[problemContentType].Example = forbiddenProblem()
	return responses
}

// enrollmentAccessCause says why [withAccess] refuses an operation with 403
// totp-required.
const enrollmentAccessCause = "An administrator without two-factor authentication may only set it up."

// documentEnrollmentAccess adds [withAccess]'s 403 totp-required to every
// operation that needs more than enrollment access, beside any 403 the
// operation sends itself. Call it once, after every operation is
// registered, before [registerProblemTypes].
func documentEnrollmentAccess(api huma.API) {
	status := strconv.Itoa(http.StatusForbidden)
	for _, op := range operations(api.OpenAPI()) {
		if operationAccess(op) <= session.AccessEnrollment {
			continue
		}

		refused, ok := op.Responses[status]
		if !ok {
			refused = problemResponses(api, http.StatusForbidden)[status]
			refused.Description = enrollmentAccessCause
			op.Responses[status] = refused
		} else {
			refused.Description += " " + enrollmentAccessCause
		}

		// One response holds every 403 as named examples.
		media := refused.Content[problemContentType]
		if media.Examples == nil {
			media.Examples = map[string]*huma.Example{}
		}
		if media.Example != nil {
			media.Examples["refused"] = &huma.Example{Value: media.Example}
			media.Example = nil
		}
		media.Examples["totpRequired"] = &huma.Example{Value: totpRequiredProblem()}
	}
}
