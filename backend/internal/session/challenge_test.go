package session

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/randomsecret"
)

func TestNewChallenge(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, time.October, 29, 12, 0, 0, 0, time.FixedZone("test", 7*60*60))}

	secret, state := newChallenge(42, clock.Now)

	raw, err := base64.RawURLEncoding.Strict().DecodeString(secret)
	require.NoError(t, err)
	assert.Len(t, raw, 32)
	hash, err := randomsecret.Hash(secret)
	require.NoError(t, err)
	assert.Equal(t, hash, state.TokenHash)
	assert.Equal(t, int64(42), state.UserID)
	assert.Zero(t, state.Failures)
	assert.Equal(t, clock.Now().UTC().Add(5*time.Minute), state.ExpiresAt)
}

func TestChallengeSecrets(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, time.October, 29, 12, 0, 0, 0, time.UTC)}

	firstSecret, first := newChallenge(42, clock.Now)
	secondSecret, second := newChallenge(42, clock.Now)

	assert.NotEqual(t, firstSecret, secondSecret)
	assert.NotEqual(t, first.TokenHash, second.TokenHash)
}

func TestChallengeExpiry(t *testing.T) {
	for _, c := range []struct {
		name string
		age  time.Duration
		want error
	}{
		{"before expiry", 5*time.Minute - time.Nanosecond, nil},
		{"at expiry", 5 * time.Minute, ErrInvalidChallenge},
		{"after expiry", 5*time.Minute + time.Nanosecond, ErrInvalidChallenge},
	} {
		t.Run(c.name, func(t *testing.T) {
			clock := &fakeClock{now: time.Date(2026, time.October, 29, 12, 0, 0, 0, time.UTC)}
			_, state := newChallenge(42, clock.Now)
			clock.Advance(c.age)

			err := checkChallenge(state, clock.Now)

			require.ErrorIs(t, err, c.want)
		})
	}
}

func TestChallengeFailures(t *testing.T) {
	for _, c := range []struct {
		name     string
		failures int
		want     error
	}{
		{"new", 0, nil},
		{"four failures", 4, nil},
		{"five failures", 5, ErrInvalidChallenge},
		{"over limit", 6, ErrInvalidChallenge},
	} {
		t.Run(c.name, func(t *testing.T) {
			clock := &fakeClock{now: time.Date(2026, time.October, 29, 12, 0, 0, 0, time.UTC)}
			_, state := newChallenge(42, clock.Now)
			state.Failures = c.failures
			original := state

			err := checkChallenge(state, clock.Now)

			require.ErrorIs(t, err, c.want)
			assert.Equal(t, original, state, "a check must not change or extend the challenge")
		})
	}
}
