package session

import (
	"context"
	"errors"

	"inbok/internal/account"
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
	User         account.User
	Access       Access
	TOTPRequired bool
}

// Authenticated is a checked session and the user's current identity.
type Authenticated struct {
	Identity Identity
	Session  Reference
}

// Current returns a checked session reference and current account identity.
// It deletes expired sessions and writes last-use time at most once an hour.
func (s *Service) Current(ctx context.Context, secret string) (Authenticated, error) {
	hash, err := hashSecret(secret)
	if err != nil {
		return Authenticated{}, ErrUnauthenticated
	}

	var userID int64
	var expired bool
	err = s.storage.UpdateSession(ctx, hash, func(state State) (Change, error) {
		checked, err := checkSession(state, s.now)
		if errors.Is(err, errExpired) {
			expired = true
			// Commit deletion before reporting an expired session to the caller.
			return Change{Action: Delete}, nil
		}
		if err != nil {
			return Change{}, err
		}

		userID = checked.UserID
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

	user, err := s.accounts.UserByID(ctx, userID)
	if errors.Is(err, account.ErrUserNotFound) {
		return Authenticated{}, ErrUnauthenticated
	}
	if err != nil {
		return Authenticated{}, err
	}
	if user.Role != account.RoleMember && user.Role != account.RoleAdmin {
		return Authenticated{}, errInvalidRole
	}

	// TOTP is not implemented yet, so neither role receives admin access.
	return Authenticated{
		Identity: Identity{User: user, Access: AccessMember, TOTPRequired: user.Role == account.RoleAdmin},
		Session:  Reference{hash: hash},
	}, nil
}
