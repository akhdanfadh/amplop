// Package session owns browser session secrets and lifetime rules.
package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"
	"uuid"
)

const (
	secretSize    = 32
	idleTimeout   = 7 * 24 * time.Hour
	maxLifetime   = 30 * 24 * time.Hour
	touchInterval = time.Hour
)

var (
	errInvalidSecret = errors.New("invalid session secret")
	errExpired       = errors.New("session is expired")
)

// State is the session data saved by storage, without the cookie secret.
// ID is the public identifier storage assigns; it is the nil UUID before insert.
type State struct {
	TokenHash  [sha256.Size]byte
	ID         uuid.UUID
	UserID     int64
	CreatedAt  time.Time
	LastUsedAt time.Time
	ExpiresAt  time.Time
	Device     Device
}

// Issued is a new session's cookie secret and the state to save in storage.
type Issued struct {
	Secret string
	State  State
}

// newSession creates a fresh secret and fixes the maximum lifetime.
// It keeps the device details of the request that creates the session.
func newSession(userID int64, device Device, now func() time.Time) Issued {
	secret, hash := newSecret()
	createdAt := now().UTC()
	return Issued{
		Secret: secret,
		State: State{
			TokenHash:  hash,
			UserID:     userID,
			CreatedAt:  createdAt,
			LastUsedAt: createdAt,
			ExpiresAt:  createdAt.Add(maxLifetime),
			Device:     cleanDevice(device),
		},
	}
}

// newSecret uses the same random-secret format for sessions and challenges.
// Their separate storage tables determine what each secret can authorize.
func newSecret() (string, [sha256.Size]byte) {
	secret := make([]byte, secretSize)
	rand.Read(secret)
	return base64.RawURLEncoding.EncodeToString(secret), sha256.Sum256(secret)
}

// hashSecret derives the lookup key from the browser's encoded secret.
func hashSecret(secret string) ([sha256.Size]byte, error) {
	if len(secret) != base64.RawURLEncoding.EncodedLen(secretSize) {
		return [sha256.Size]byte{}, errInvalidSecret
	}

	// Even strict base64 ignores newlines, so verify the decoded size too.
	raw, err := base64.RawURLEncoding.Strict().DecodeString(secret)
	if err != nil || len(raw) != secretSize {
		return [sha256.Size]byte{}, errInvalidSecret
	}
	return sha256.Sum256(raw), nil
}

// Expiry is the expiry rule at one moment, as two limits storage can
// compare in SQL: a session is expired when ExpiresAt is at or before
// ExpiresBy, or LastUsedAt is at or before LastUsedBy.
type Expiry struct {
	ExpiresBy  time.Time
	LastUsedBy time.Time
}

func expiryAt(now time.Time) Expiry {
	now = now.UTC()
	return Expiry{ExpiresBy: now, LastUsedBy: now.Add(-idleTimeout)}
}

func (e Expiry) expired(state State) bool {
	return !state.ExpiresAt.After(e.ExpiresBy) || !state.LastUsedAt.After(e.LastUsedBy)
}

// checkSession rejects expired state before any last-use update.
func checkSession(state State, now func() time.Time) (State, error) {
	checkedAt := now()
	if expiryAt(checkedAt).expired(state) {
		return State{}, errExpired
	}

	if checkedAt.Sub(state.LastUsedAt) >= touchInterval {
		state.LastUsedAt = checkedAt.UTC()
	}
	return state, nil
}
