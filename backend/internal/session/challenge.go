package session

import (
	"crypto/sha256"
	"errors"
	"time"

	"inbok/internal/randomsecret"
)

const (
	// Product policy: limit the time and guesses allowed after password proof.
	challengeLifetime     = 5 * time.Minute
	challengeFailureLimit = 5
)

// ErrInvalidChallenge requires password sign-in again, without revealing why.
var ErrInvalidChallenge = errors.New("invalid sign-in challenge")

// Challenge is pending second-factor proof, never an authenticated session.
// [Storage] receives the hash; only the browser receives the secret.
type Challenge struct {
	TokenHash [sha256.Size]byte
	UserID    int64
	ExpiresAt time.Time
	Failures  int
}

func newChallenge(userID int64, now func() time.Time) (string, Challenge) {
	secret, hash := randomsecret.New()
	return secret, Challenge{
		TokenHash: hash,
		UserID:    userID,
		ExpiresAt: now().UTC().Add(challengeLifetime),
	}
}

// checkChallenge never extends expiry or resets failures during a retry.
// [Storage] must check and update the challenge under the same lock.
func checkChallenge(state Challenge, now func() time.Time) error {
	if !now().Before(state.ExpiresAt) || state.Failures >= challengeFailureLimit {
		return ErrInvalidChallenge
	}
	return nil
}
