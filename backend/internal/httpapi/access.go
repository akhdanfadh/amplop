package httpapi

import (
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"

	"inbok/internal/account"
	"inbok/internal/session"
)

// accessKey names the shared operation metadata entry for required access.
const accessKey = "access"

// identityKey identifies the checked user stored in the request context.
type identityKey struct{}

// withAccess enforces each operation's access level before parsing its inputs.
// Missing or invalid access metadata requires admin access.
func withAccess(api huma.API, service sessionService) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		level, ok := ctx.Operation().Metadata[accessKey].(session.Access)
		if !ok || level < session.AccessPublic || level > session.AccessAdmin {
			level = session.AccessAdmin
		}
		if level == session.AccessPublic {
			next(ctx)
			return
		}

		// Keep account-specific responses out of browser and proxy HTTP caches.
		ctx.SetHeader("Cache-Control", "no-store")
		cookie, cookieErr := huma.ReadCookie(ctx, sessionCookieName)
		var identity session.Identity
		err := session.ErrUnauthenticated
		if cookieErr == nil && cookie.Value != "" {
			identity, err = service.Current(ctx.Context(), cookie.Value)
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

		allowed := identity.Access >= session.AccessMember && identity.Access <= session.AccessAdmin &&
			(identity.User.Role == account.RoleMember || identity.User.Role == account.RoleAdmin)
		if level == session.AccessAdmin && identity.User.Role == account.RoleAdmin && identity.TOTPRequired {
			writeProblem(humagin.Unwrap(ctx), newProblem(http.StatusForbidden, problemPrefix+"totp-required",
				"TOTP required", "Set up two-factor authentication to use administrator features."))
			return
		}
		if !allowed || identity.Access < level || (level == session.AccessAdmin && identity.User.Role != account.RoleAdmin) {
			writeProblem(humagin.Unwrap(ctx), newProblem(http.StatusForbidden, problemPrefix+"forbidden",
				"Access denied", "You do not have access to this operation."))
			return
		}
		next(huma.WithValue(ctx, identityKey{}, identity))
	}
}

// unauthenticatedProblem is the safe response for a missing or invalid session.
func unauthenticatedProblem() *problem {
	return newProblem(http.StatusUnauthorized, problemPrefix+"unauthenticated", "Sign-in required", "Sign in to continue.")
}
