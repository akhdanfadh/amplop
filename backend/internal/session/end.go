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

// EndOthers ends every session of the user except the current one.
// It returns how many of them were still live; expired rows go uncounted.
func (s *Service) EndOthers(ctx context.Context, current Authenticated) (int, error) {
	userID := current.Identity.User.ID
	if current.Session == (Reference{}) || userID <= 0 {
		return 0, ErrUnauthenticated
	}

	deleted, err := s.storage.DeleteUserOtherSessions(ctx, userID, current.Session.hash, func(state State) error {
		// Current's earlier check can become stale: a session that expired
		// since then must not end the others.
		if _, err := checkSession(state, s.now); err != nil {
			return ErrUnauthenticated
		}
		return nil
	})
	// The current session was ended since Current checked it.
	if errors.Is(err, ErrNotFound) {
		return 0, ErrUnauthenticated
	}
	if err != nil {
		return 0, err
	}
	ended := 0
	for _, state := range deleted {
		if _, err := checkSession(state, s.now); err == nil {
			ended++
		}
	}
	return ended, nil
}
