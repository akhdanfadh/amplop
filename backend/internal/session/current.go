package session

import (
	"context"
	"errors"
	"uuid"

	"inbok/internal/account"
	"inbok/internal/randomsecret"
)

// Access is a request's allowed or required level, ordered by privilege.
type Access int

const (
	AccessPublic Access = iota
	AccessMember
	AccessAdmin
)

// ErrUnauthenticated means no valid session identifies a current user.
var ErrUnauthenticated = errors.New("unauthenticated")

var errInvalidRole = errors.New("invalid user role")

// Identity is the current user and the access allowed for this request.
// An administrator without confirmed TOTP has member access.
type Identity struct {
	User          account.User
	Access        Access
	TOTPRequired  bool
	EmailVerified bool
}

// IdentityState is the current account, factor, and email verification state
// for an existing session.
type IdentityState struct {
	User          account.User
	TOTPConfirmed bool
	EmailVerified bool
}

// Authenticated is a checked session and the user's current identity.
type Authenticated struct {
	Identity Identity
	Session  Reference
}

// Current returns a checked session reference and current account identity.
// It deletes expired sessions and writes last-use time at most once an hour.
func (s *Service) Current(ctx context.Context, secret string) (Authenticated, error) {
	// 1. Refuse a malformed secret before any storage.
	hash, err := randomsecret.Hash(secret)
	if err != nil {
		return Authenticated{}, ErrUnauthenticated
	}

	// 2. Check the session under its lock: delete it when expired, or save
	// its last use.
	var expired bool
	var id uuid.UUID
	err = s.storage.UpdateSession(ctx, hash, func(state State) (Change, error) {
		id = state.ID
		checked, err := checkSession(state, s.now)
		if errors.Is(err, errExpired) {
			expired = true
			// Commit deletion before reporting an expired session to the caller.
			return Change{Action: Delete}, nil
		}
		if err != nil {
			return Change{}, err
		}

		if !checked.LastUsedAt.Equal(state.LastUsedAt) {
			return Change{Action: Touch, LastUsedAt: checked.LastUsedAt}, nil
		}
		return Change{Action: Keep}, nil
	})
	if errors.Is(err, ErrNotFound) {
		return Authenticated{}, ErrUnauthenticated
	}
	if err != nil {
		return Authenticated{}, err
	}
	if expired {
		return Authenticated{}, ErrUnauthenticated
	}

	// 3. Read the user's identity, only for a session still live.
	state, err := s.storage.ReadSessionIdentity(ctx, hash)
	if errors.Is(err, ErrNotFound) {
		return Authenticated{}, ErrUnauthenticated
	}
	if err != nil {
		return Authenticated{}, err
	}
	user := state.User
	if user.Role != account.RoleMember && user.Role != account.RoleAdmin {
		return Authenticated{}, errInvalidRole
	}

	// Role alone is insufficient; a confirmed factor is also required.
	identity := Identity{User: user, Access: AccessMember, EmailVerified: state.EmailVerified}
	if user.Role == account.RoleAdmin {
		identity.TOTPRequired = !state.TOTPConfirmed
		if state.TOTPConfirmed {
			identity.Access = AccessAdmin
		}
	}

	return Authenticated{Identity: identity, Session: Reference{hash: hash, id: id}}, nil
}
