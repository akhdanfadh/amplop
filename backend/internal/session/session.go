// Package session owns browser session secrets and lifetime rules.
package session

import (
	"crypto/sha256"
	"errors"
	"time"
	"uuid"

	"amplop/internal/randomsecret"
)

const (
	idleTimeout   = 7 * 24 * time.Hour
	maxLifetime   = 30 * 24 * time.Hour
	touchInterval = time.Hour

	// enrollmentSessionLifetime is the fixed lifetime of a session with
	// enrollment access. Product policy: enough to set up two-factor
	// authentication, matching the enrollment's own limit; a forgotten one
	// ends soon.
	enrollmentSessionLifetime = 15 * time.Minute
)

var errExpired = errors.New("session is expired")

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

// newSession creates a fresh secret that lives at most lifetime, normally
// [maxLifetime]. It keeps the device details of the request that creates
// the session.
func newSession(userID int64, device Device, lifetime time.Duration, now func() time.Time) Issued {
	secret, hash := randomsecret.New()
	createdAt := now().UTC()
	return Issued{
		Secret: secret,
		State: State{
			TokenHash:  hash,
			UserID:     userID,
			CreatedAt:  createdAt,
			LastUsedAt: createdAt,
			ExpiresAt:  createdAt.Add(lifetime),
			Device:     cleanDevice(device),
		},
	}
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
