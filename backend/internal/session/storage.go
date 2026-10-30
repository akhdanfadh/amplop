package session

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"
)

// ErrNotFound means the session row does not exist.
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
	// ReadSessionIdentity loads account and factor metadata in one snapshot,
	// rechecking the session so enrollment revocation cannot grant old proof access.
	ReadSessionIdentity(ctx context.Context, hash [sha256.Size]byte) (IdentityState, error)

	// StartSignIn locks the owner and reads the current password hash and factor.
	// It saves the chosen session or challenge and prunes expired challenges atomically.
	StartSignIn(ctx context.Context, userID int64, update func(SignInState) (SignInChange, error)) error

	// UpdateSession applies a Go decision to a session in one transaction.
	// It locks and loads the row, calls update, then saves its decision.
	// An error rolls back the change.
	// Keep performs no write. A missing row returns ErrNotFound.
	UpdateSession(ctx context.Context, hash [sha256.Size]byte, update func(State) (Change, error)) error

	// DeleteSession succeeds even when the row is already missing.
	DeleteSession(ctx context.Context, hash [sha256.Size]byte) error
}
