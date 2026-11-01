package session

import (
	"encoding/base64"
	"encoding/hex"
	"net/netip"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func TestNewSession(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)}

	issued := newSession(7, Device{}, clock.Now)

	assert.Regexp(t, `^[A-Za-z0-9_-]{43}$`, issued.Secret)
	secret, err := base64.RawURLEncoding.Strict().DecodeString(issued.Secret)
	require.NoError(t, err)
	assert.Len(t, secret, 32)
	assert.Equal(t, int64(7), issued.State.UserID)
	assert.Equal(t, uuid.Nil(), issued.State.ID, "storage assigns the public ID")
	assert.Equal(t, clock.now, issued.State.CreatedAt)
	assert.Equal(t, clock.now, issued.State.LastUsedAt)
	assert.Equal(t, time.Date(2026, time.November, 26, 12, 0, 0, 0, time.UTC), issued.State.ExpiresAt)
}

func TestNewSessionDevice(t *testing.T) {
	device := Device{UserAgent: "agent\xff", Addr: netip.MustParseAddr("::ffff:192.0.2.1")}

	issued := newSession(7, device, time.Now)

	assert.Equal(t, Device{UserAgent: "agent", Addr: netip.MustParseAddr("192.0.2.1")}, issued.State.Device)
}

func TestNewSessionSecrets(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)}

	first := newSession(7, Device{}, clock.Now)
	second := newSession(7, Device{}, clock.Now)
	hash, err := hashSecret(first.Secret)
	require.NoError(t, err)

	assert.NotEqual(t, first.Secret, second.Secret)
	assert.NotEqual(t, first.State.TokenHash, second.State.TokenHash)
	assert.Equal(t, first.State.TokenHash, hash)
}

// TestHashSecret checks SHA-256 against a known vector of 32 zero bytes.
// The digest was checked independently with Python's hashlib.
func TestHashSecret(t *testing.T) {
	hash, err := hashSecret(strings.Repeat("A", 43))

	require.NoError(t, err)
	assert.Equal(t, "66687aadf862bd776c8fc18b8e9f8e20089714856ee233b3902a591d0d5f2925", hex.EncodeToString(hash[:]))
}

func TestHashSecretInvalid(t *testing.T) {
	cases := []struct {
		name, secret string
	}{
		{"empty", ""},
		{"short", strings.Repeat("A", 42)},
		{"long", strings.Repeat("A", 44)},
		{"oversized", strings.Repeat("A", 65536)},
		{"padding", strings.Repeat("A", 43) + "="},
		{"standard alphabet", strings.Repeat("/", 43)},
		{"noncanonical", strings.Repeat("A", 42) + "B"},
		{"newline", strings.Repeat("A", 43) + "\n"},
		{"newline within expected length", strings.Repeat("A", 42) + "\n"},
		{"embedded newline", strings.Repeat("A", 21) + "\r\n" + strings.Repeat("A", 22)},
		{"leading space", " " + strings.Repeat("A", 43)},
		{"trailing space", strings.Repeat("A", 43) + " "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hash, err := hashSecret(c.secret)

			require.ErrorIs(t, err, errInvalidSecret)
			require.EqualError(t, err, "invalid session secret")
			assert.Zero(t, hash)
		})
	}
}

func TestCheckSessionIdle(t *testing.T) {
	cases := []struct {
		name    string
		age     time.Duration
		expired bool
	}{
		{"before limit", 7*24*time.Hour - time.Nanosecond, false},
		{"at limit", 7 * 24 * time.Hour, true},
		{"after limit", 7*24*time.Hour + time.Nanosecond, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clock := &fakeClock{now: time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)}
			issued := newSession(7, Device{}, clock.Now)
			clock.Advance(c.age)

			state, err := checkSession(issued.State, clock.Now)

			if c.expired {
				require.ErrorIs(t, err, errExpired)
				assert.Zero(t, state)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, issued.State.UserID, state.UserID)
			assert.Equal(t, issued.State.ExpiresAt, state.ExpiresAt)
		})
	}
}

func TestCheckSessionMax(t *testing.T) {
	cases := []struct {
		name    string
		age     time.Duration
		expired bool
	}{
		{"before limit", 30*24*time.Hour - time.Nanosecond, false},
		{"at limit", 30 * 24 * time.Hour, true},
		{"after limit", 30*24*time.Hour + time.Nanosecond, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clock := &fakeClock{now: time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)}
			issued := newSession(7, Device{}, clock.Now)
			// Recent activity must not extend the fixed lifetime.
			issued.State.LastUsedAt = clock.now.Add(29 * 24 * time.Hour)
			clock.Advance(c.age)

			state, err := checkSession(issued.State, clock.Now)

			if c.expired {
				require.ErrorIs(t, err, errExpired)
				assert.Zero(t, state)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, issued.State.UserID, state.UserID)
			assert.Equal(t, issued.State.ExpiresAt, state.ExpiresAt)
		})
	}
}

func TestCheckSessionTouch(t *testing.T) {
	cases := []struct {
		name          string
		age, lastUsed time.Duration
	}{
		{"clock moved back", -time.Hour, 0},
		{"just used", 0, 0},
		{"before interval", time.Hour - time.Nanosecond, 0},
		{"at interval", time.Hour, time.Hour},
		{"after interval", time.Hour + time.Nanosecond, time.Hour + time.Nanosecond},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clock := &fakeClock{now: time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)}
			issued := newSession(7, Device{}, clock.Now)
			clock.Advance(c.age)

			state, err := checkSession(issued.State, clock.Now)
			require.NoError(t, err)

			want := issued.State
			want.LastUsedAt = want.LastUsedAt.Add(c.lastUsed)
			assert.Equal(t, want, state)
		})
	}
}

func TestCheckSessionActivity(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)}
	issued := newSession(7, Device{}, clock.Now)
	state := issued.State

	// Activity every six days keeps the idle deadline from being reached.
	for range 4 {
		clock.Advance(6 * 24 * time.Hour)
		updated, err := checkSession(state, clock.Now)
		require.NoError(t, err)

		assert.Equal(t, clock.now, updated.LastUsedAt)
		assert.Equal(t, issued.State.ExpiresAt, updated.ExpiresAt)
		state = updated
	}

	// Day 30 still expires the session despite the recent activity.
	clock.Advance(6 * 24 * time.Hour)
	_, err := checkSession(state, clock.Now)

	require.ErrorIs(t, err, errExpired)
}
