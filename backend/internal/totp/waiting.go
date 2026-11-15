package totp

import (
	"context"
	"crypto/sha256"
	"time"

	"inbok/internal/randomsecret"
)

// waitingLifetime bounds a waiting replacement set from the code check that
// made it. Product policy: long enough to save the codes, short enough that
// a forgotten set does not wait for long.
const waitingLifetime = 15 * time.Minute

// waitingSet is a new backup-code set ready to wait: the codes to show, their
// hashes, and a one-time token with its hash.
type waitingSet struct {
	set       BackupCodes
	token     string
	tokenHash [sha256.Size]byte
}

// newWaitingSet makes ten backup codes and the token that activates them. It
// returns the context's error if ctx ends while waiting to hash.
func newWaitingSet(ctx context.Context) (waitingSet, error) {
	set, err := NewBackupCodes(ctx)
	if err != nil {
		return waitingSet{}, err
	}
	token, tokenHash := randomsecret.New()
	return waitingSet{set: set, token: token, tokenHash: tokenHash}, nil
}

// waiting returns the stored form of the set, expiring at expiresAt.
func (w waitingSet) waiting(expiresAt time.Time) *Waiting {
	return &Waiting{
		Hashes:    w.set.Hashes,
		TokenHash: w.tokenHash,
		ExpiresAt: expiresAt.UTC(),
	}
}

// codes returns what the person sees: the codes and the token.
func (w waitingSet) codes() Codes {
	return Codes{Codes: w.set.Codes, Token: w.token}
}
