package session

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"
	"uuid"
)

// ErrNotFound means no session row matches. Ending a session by ID also
// returns it for an expired row or another user's row, and reading an
// identity or confirming TOTP returns it for a deactivated user.
var ErrNotFound = errors.New("session not found")

// Action identifies whether to keep, touch, or delete a session.
type Action int

const (
	Keep Action = iota
	Touch
	Delete
)

// Change is the storage action chosen by a session check.
// It permits a last-use update or deletion, never expiry extension.
type Change struct {
	Action     Action
	LastUsedAt time.Time
}

// Storage stores session hashes and lifetimes.
// Updates serialize decisions for each session.
type Storage interface {
	// ReadSessionIdentity loads account, factor, and email verification state in
	// one snapshot, rechecking the session so enrollment revocation cannot grant
	// old proof access. A missing session, or a deactivated user's, returns
	// [ErrNotFound].
	ReadSessionIdentity(ctx context.Context, hash [sha256.Size]byte) (IdentityState, error)

	// StartSignIn locks the owner and reads the current password hash, factor,
	// and whether the owner is deactivated.
	// It saves the chosen session or challenge and deletes expired challenges
	// atomically; saving a session also deletes the user's expired sessions.
	StartSignIn(ctx context.Context, userID int64, update func(SignInState) (SignInChange, error)) error

	// UpdateSession applies a Go decision to a session in one transaction.
	// It locks and loads the row, calls update, then saves its decision.
	// An error rolls back the change.
	// [Keep] performs no write. A missing row returns [ErrNotFound].
	UpdateSession(ctx context.Context, hash [sha256.Size]byte, update func(State) (Change, error)) error

	// DeleteSession succeeds even when the row is already missing.
	DeleteSession(ctx context.Context, hash [sha256.Size]byte) error

	// ReadUserSessions returns every session row the user owns, newest
	// sign-in first, including expired rows the Go rules have not deleted yet.
	ReadUserSessions(ctx context.Context, userID int64) ([]State, error)

	// UpdateUserSession is [Storage.UpdateSession] for the user's session with
	// this public ID. Another user's session returns [ErrNotFound], like a missing
	// one.
	UpdateUserSession(ctx context.Context, userID int64, id uuid.UUID, update func(State) (Change, error)) error

	// DeleteUserOtherSessions locks the user, then the session keep, and calls
	// check with keep's state. If check accepts it, one statement deletes the
	// user's other sessions, and they are returned, expired ones included.
	// A missing user or keep returns [ErrNotFound]. An error deletes nothing.
	DeleteUserOtherSessions(ctx context.Context, userID int64, keep [sha256.Size]byte, check func(State) error) ([]State, error)
}
