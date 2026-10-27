package session

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"
)

// ErrNotFound means the session row does not exist.
var ErrNotFound = errors.New("session not found")

// Action is the change decided while a session row is locked.
type Action int

const (
	Keep Action = iota
	Touch
	Delete
)

// Change only permits a last-use update or deletion, never expiry extension.
type Change struct {
	Action     Action
	LastUsedAt time.Time
}

// Storage saves session hashes and serializes decisions for each session.
type Storage interface {
	InsertSession(ctx context.Context, state State) error

	// UpdateSession locks and loads the row, calls update, then saves its
	// decision in one transaction. An error rolls back the change.
	// Keep performs no write. A missing row returns ErrNotFound.
	UpdateSession(ctx context.Context, hash [sha256.Size]byte, update func(State) (Change, error)) error

	// DeleteSession succeeds even when the row is already missing.
	DeleteSession(ctx context.Context, hash [sha256.Size]byte) error
}
