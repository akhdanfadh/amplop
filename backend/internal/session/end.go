package session

import (
	"context"
	"errors"
	"uuid"
)

// ErrCurrentSession means the request tried to end its own session by ID.
// Sign-out ends the current session and clears its cookie.
var ErrCurrentSession = errors.New("current session cannot be ended by ID")

// End ends one of the user's other sessions. A missing, expired, or another
// user's session returns ErrNotFound, so the caller cannot tell them apart.
func (s *Service) End(ctx context.Context, current Authenticated, id uuid.UUID) error {
	userID := current.Identity.User.ID
	if current.Session == (Reference{}) || userID <= 0 {
		return ErrUnauthenticated
	}

	var expired bool
	err := s.storage.UpdateUserSession(ctx, userID, id, func(state State) (Change, error) {
		if state.TokenHash == current.Session.hash {
			return Change{}, ErrCurrentSession
		}
		// An expired row is already ended; delete it but report it as missing.
		if _, err := checkSession(state, s.now); err != nil {
			expired = true
		}
		return Change{Action: Delete}, nil
	})
	if err != nil {
		return err
	}
	if expired {
		return ErrNotFound
	}
	return nil
}
