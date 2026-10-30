package session

import (
	"errors"
	"slices"
	"testing"

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
