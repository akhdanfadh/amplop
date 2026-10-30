package session

import (
	"errors"
	"slices"
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnd(t *testing.T) {
	f := newSessionsFixture()

	err := f.service.End(t.Context(), f.current, sessionID(2))

	require.NoError(t, err)
	assert.Equal(t, int64(42), f.store.userID)
	require.Len(t, f.store.deleted, 1)
	assert.Equal(t, sessionID(2), f.store.deleted[0].ID)
	assert.Len(t, f.store.rows, 3)
}

func TestEndCurrent(t *testing.T) {
	f := newSessionsFixture()

	err := f.service.End(t.Context(), f.current, sessionID(1))

	require.ErrorIs(t, err, ErrCurrentSession)
	assert.Empty(t, f.store.deleted)
}

func TestEndNotFound(t *testing.T) {
	cases := []struct {
		name    string
		id      byte
		deleted int
	}{
		{"missing", 99, 0},
		{"another user's", 4, 0},
		// An expired row is already ended; it is deleted but reported missing.
		{"expired", 3, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newSessionsFixture()

			err := f.service.End(t.Context(), f.current, sessionID(c.id))

			require.ErrorIs(t, err, ErrNotFound)
			assert.Len(t, f.store.deleted, c.deleted)
			assert.True(t, slices.ContainsFunc(f.store.rows, func(row State) bool { return row.ID == sessionID(4) }),
				"another user's session is untouched")
		})
	}
}

func TestEndErrors(t *testing.T) {
	lost := errors.New("connection lost")
	f := newSessionsFixture()
	f.store.err = lost

	err := f.service.End(t.Context(), f.current, sessionID(2))

	require.ErrorIs(t, err, lost)

	for _, current := range []Authenticated{{}, {Identity: f.current.Identity}, {Session: f.current.Session}} {
		f := newSessionsFixture()

		err := f.service.End(t.Context(), current, sessionID(2))

		require.ErrorIs(t, err, ErrUnauthenticated)
		assert.Zero(t, f.store.userID, "an unchecked session reaches no storage")
	}
}

func TestEndOthers(t *testing.T) {
	f := newSessionsFixture()

	ended, err := f.service.EndOthers(t.Context(), f.current)

	require.NoError(t, err)
	assert.Equal(t, 1, ended, "the expired session is deleted but not counted")
	assert.Equal(t, int64(42), f.store.userID)
	assert.Equal(t, f.current.Session.hash, f.store.keep)
	require.Len(t, f.store.deleted, 2)
	var kept []uuid.UUID
	for _, row := range f.store.rows {
		kept = append(kept, row.ID)
	}
	assert.ElementsMatch(t, []uuid.UUID{sessionID(1), sessionID(4)}, kept)
}

func TestEndOthersIdleLimit(t *testing.T) {
	f := newSessionsFixture()
	// Session 2 reaches the idle limit exactly now, so it is no longer live.
	f.store.rows[0].LastUsedAt = f.clock.now.Add(-idleTimeout)

	ended, err := f.service.EndOthers(t.Context(), f.current)

	require.NoError(t, err)
	assert.Zero(t, ended)
	assert.Len(t, f.store.deleted, 2)
}

func TestEndOthersStaleCurrent(t *testing.T) {
	cases := []struct {
		name  string
		stale func(*sessionsFixture)
	}{
		// Another browser ended the current session after its request was checked.
		{"ended", func(f *sessionsFixture) { f.store.rows = slices.Delete(f.store.rows, 1, 2) }},
		{"expired", func(f *sessionsFixture) { f.clock.Advance(maxLifetime) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newSessionsFixture()
			c.stale(&f)
			before := slices.Clone(f.store.rows)

			ended, err := f.service.EndOthers(t.Context(), f.current)

			require.ErrorIs(t, err, ErrUnauthenticated)
			assert.Zero(t, ended)
			assert.Equal(t, before, f.store.rows, "a stale session ends nothing")
		})
	}
}

func TestEndOthersErrors(t *testing.T) {
	lost := errors.New("connection lost")
	f := newSessionsFixture()
	f.store.err = lost

	ended, err := f.service.EndOthers(t.Context(), f.current)

	require.ErrorIs(t, err, lost)
	assert.Zero(t, ended)

	for _, current := range []Authenticated{{}, {Identity: f.current.Identity}, {Session: f.current.Session}} {
		f := newSessionsFixture()

		ended, err := f.service.EndOthers(t.Context(), current)

		require.ErrorIs(t, err, ErrUnauthenticated)
		assert.Zero(t, ended)
		assert.Zero(t, f.store.userID, "an unchecked session reaches no storage")
	}
}
