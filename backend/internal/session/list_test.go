package session

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/netip"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/account"
)

const (
	chromeOnMac    = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
	firefoxOnWin   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:131.0) Gecko/20100101 Firefox/131.0"
	safariOnIPhone = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1"
)

// memorySessions keeps session rows for the list and end rules.
type memorySessions struct {
	Storage
	rows    []State
	err     error
	userID  int64
	keep    [sha256.Size]byte
	deleted []State
}

func (f *memorySessions) ReadUserSessions(_ context.Context, userID int64) ([]State, error) {
	f.userID = userID
	var owned []State
	for _, row := range f.rows {
		if row.UserID == userID {
			owned = append(owned, row)
		}
	}
	return owned, f.err
}

func (f *memorySessions) UpdateUserSession(_ context.Context, userID int64, id uuid.UUID, update func(State) (Change, error)) error {
	f.userID = userID
	if f.err != nil {
		return f.err
	}
	for i, row := range f.rows {
		if row.UserID != userID || row.ID != id {
			continue
		}
		change, err := update(row)
		if err != nil {
			return err
		}
		if change.Action == Delete {
			f.deleted = append(f.deleted, row)
			f.rows = append(f.rows[:i:i], f.rows[i+1:]...)
		}
		return nil
	}
	return ErrNotFound
}

func (f *memorySessions) DeleteUserOtherSessions(_ context.Context, userID int64, keep [sha256.Size]byte, check func(State) error) ([]State, error) {
	f.userID, f.keep = userID, keep
	if f.err != nil {
		return nil, f.err
	}
	i := slices.IndexFunc(f.rows, func(row State) bool { return row.UserID == userID && row.TokenHash == keep })
	if i < 0 {
		return nil, ErrNotFound
	}
	if err := check(f.rows[i]); err != nil {
		return nil, err
	}
	var kept, deleted []State
	for _, row := range f.rows {
		if row.UserID == userID && row.TokenHash != keep {
			deleted = append(deleted, row)
		} else {
			kept = append(kept, row)
		}
	}
	f.rows, f.deleted = kept, deleted
	return deleted, nil
}

// sessionsFixture holds the current session, a live and an expired other
// session of user 42, and a session of user 43, in storage's order.
type sessionsFixture struct {
	service *Service
	store   *memorySessions
	current Authenticated
	clock   *fakeClock
}

// sessionID makes a readable fixture ID whose last byte is n.
func sessionID(n byte) uuid.UUID {
	return uuid.UUID{15: n}
}

func newSessionsFixture() sessionsFixture {
	clock := &fakeClock{now: time.Date(2026, time.October, 30, 12, 0, 0, 0, time.UTC)}
	row := func(id byte, userID int64, created time.Duration, device Device) State {
		createdAt := clock.now.Add(-created)
		return State{
			TokenHash: sha256.Sum256([]byte{id}), ID: sessionID(id), UserID: userID,
			CreatedAt: createdAt, LastUsedAt: createdAt, ExpiresAt: createdAt.Add(maxLifetime), Device: device,
		}
	}
	store := &memorySessions{rows: []State{
		row(2, 42, time.Hour, Device{UserAgent: firefoxOnWin, Addr: netip.MustParseAddr("2001:db8::2")}),
		row(1, 42, 3*time.Hour, Device{UserAgent: chromeOnMac, Addr: netip.MustParseAddr("192.0.2.1")}),
		row(3, 42, 8*24*time.Hour, Device{UserAgent: safariOnIPhone}),
		row(4, 43, time.Hour, Device{UserAgent: chromeOnMac}),
	}}
	current := Authenticated{
		Identity: Identity{User: account.User{ID: 42}, Access: AccessMember},
		Session:  Reference{hash: store.rows[1].TokenHash},
	}
	return sessionsFixture{service: NewService(store, nil, nil, clock.Now), store: store, current: current, clock: clock}
}

func TestList(t *testing.T) {
	f := newSessionsFixture()
	// A session from before device capture has no details.
	f.store.rows = slices.Insert(f.store.rows, 1, State{
		TokenHash: sha256.Sum256([]byte("old")), ID: sessionID(5), UserID: 42,
		CreatedAt: f.clock.now.Add(-2 * time.Hour), LastUsedAt: f.clock.now.Add(-30 * time.Minute),
		ExpiresAt: f.clock.now.Add(maxLifetime),
	})

	listed, err := f.service.List(t.Context(), f.current)

	require.NoError(t, err)
	assert.Equal(t, int64(42), f.store.userID)
	assert.Equal(t, []Listed{
		{
			ID: sessionID(2), Browser: "Firefox", OS: "Windows", SignedInFrom: netip.MustParseAddr("2001:db8::2"),
			CreatedAt: f.clock.now.Add(-time.Hour), LastUsedAt: f.clock.now.Add(-time.Hour),
		},
		{ID: sessionID(5), CreatedAt: f.clock.now.Add(-2 * time.Hour), LastUsedAt: f.clock.now.Add(-30 * time.Minute)},
		{
			ID: sessionID(1), Browser: "Chrome", OS: "MacOS", SignedInFrom: netip.MustParseAddr("192.0.2.1"),
			CreatedAt: f.clock.now.Add(-3 * time.Hour), LastUsedAt: f.clock.now.Add(-3 * time.Hour), Current: true,
		},
	}, listed)
	assert.Len(t, f.store.rows, 5, "listing deletes nothing")
}

func TestListExpiry(t *testing.T) {
	cases := []struct {
		name     string
		lastUsed time.Duration
		created  time.Duration
		listed   bool
	}{
		{"idle just under the limit", 7*24*time.Hour - time.Nanosecond, 7*24*time.Hour - time.Nanosecond, true},
		{"idle at the limit", 7 * 24 * time.Hour, 7 * 24 * time.Hour, false},
		{"recently used just under maximum lifetime", time.Minute, 30*24*time.Hour - time.Nanosecond, true},
		{"recently used but at maximum lifetime", time.Minute, 30 * 24 * time.Hour, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newSessionsFixture()
			f.store.rows[0].CreatedAt = f.clock.now.Add(-c.created)
			f.store.rows[0].ExpiresAt = f.store.rows[0].CreatedAt.Add(maxLifetime)
			f.store.rows[0].LastUsedAt = f.clock.now.Add(-c.lastUsed)

			listed, err := f.service.List(t.Context(), f.current)

			require.NoError(t, err)
			var ids []uuid.UUID
			for _, l := range listed {
				ids = append(ids, l.ID)
			}
			want := []uuid.UUID{sessionID(1)}
			if c.listed {
				want = []uuid.UUID{sessionID(1), sessionID(2)}
			}
			assert.ElementsMatch(t, want, ids, "the fixture's idle session 3 stays hidden")
		})
	}
}

func TestListErrors(t *testing.T) {
	lost := errors.New("connection lost")
	f := newSessionsFixture()
	f.store.err = lost

	listed, err := f.service.List(t.Context(), f.current)

	require.ErrorIs(t, err, lost)
	assert.Nil(t, listed)

	for _, current := range []Authenticated{{}, {Identity: f.current.Identity}, {Session: f.current.Session}} {
		f := newSessionsFixture()

		listed, err := f.service.List(t.Context(), current)

		require.ErrorIs(t, err, ErrUnauthenticated)
		assert.Nil(t, listed)
		assert.Zero(t, f.store.userID, "an unchecked session reads nothing")
	}
}

func TestDescribeUserAgent(t *testing.T) {
	cases := []struct {
		name, agent, browser, os string
	}{
		{"Chrome on macOS", chromeOnMac, "Chrome", "MacOS"},
		{"Firefox on Windows", firefoxOnWin, "Firefox", "Windows"},
		{"Safari on iOS", safariOnIPhone, "Safari", "iOS"},
		{"Edge on Windows", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0", "Edge", "Windows"},
		{"Firefox on Linux", "Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0", "Firefox", "Linux"},
		{"empty", "", "", ""},
		{"not a browser", "curl/8.10.1", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			browser, os := describeUserAgent(c.agent)

			assert.Equal(t, c.browser, browser)
			assert.Equal(t, c.os, os)
		})
	}
}
