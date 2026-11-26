package postgres

import (
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/connectedaccount"
)

// insertConnectingSession adds a session for userID and returns its public
// ID.
func insertConnectingSession(t *testing.T, conn *pgx.Conn, userID int64) uuid.UUID {
	t.Helper()
	token := sha256.Sum256([]byte(uuid.New().String()))
	var id uuid.UUID
	require.NoError(t, conn.QueryRow(t.Context(),
		"INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, now() + interval '1 day') RETURNING id",
		token[:], userID).Scan(&id))
	return id
}

// countConnectStates returns how many connect states are saved.
func countConnectStates(t *testing.T, conn *pgx.Conn) int64 {
	t.Helper()
	var count int64
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM connect_states").Scan(&count))
	return count
}

// newConnectState returns a state for userID's session, reconnecting
// accountID unless it is 0.
func newConnectState(userID int64, session uuid.UUID, accountID int64) connectedaccount.ConnectState {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return connectedaccount.ConnectState{
		StateHash:    sha256.Sum256([]byte(uuid.New().String())),
		UserID:       userID,
		SessionID:    session,
		AccountID:    accountID,
		CodeVerifier: "verifier-1",
		CreatedAt:    now,
		ExpiresAt:    now.Add(time.Hour),
	}
}

// saveConnectState saves state as a start of its user that the throttle
// lets through.
func saveConnectState(t *testing.T, db *DB, state connectedaccount.ConnectState) error {
	t.Helper()
	return db.InsertConnectState(t.Context(), state.UserID, func([]time.Time) (connectedaccount.ConnectState, error) {
		return state, nil
	})
}

func TestConnectStateStorage(t *testing.T) {
	db, conn := setupStorage(t)
	member := insertConnectingMember(t, conn, "member@example.com")
	state := newConnectState(member, insertConnectingSession(t, conn, member), 0)
	require.NoError(t, saveConnectState(t, db, state))

	taken, err := db.DeleteConnectState(t.Context(), state.StateHash)
	require.NoError(t, err)

	assert.Equal(t, state, taken)
}

// Two callbacks with one state at once: one takes it, the other finds it
// gone.
func TestConnectStateTakenOnce(t *testing.T) {
	db, conn := setupStorage(t)
	member := insertConnectingMember(t, conn, "member@example.com")
	state := newConnectState(member, insertConnectingSession(t, conn, member), 0)
	require.NoError(t, saveConnectState(t, db, state))

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Go(func() {
			_, errs[i] = db.DeleteConnectState(t.Context(), state.StateHash)
		})
	}
	wg.Wait()

	taken := 0
	for _, err := range errs {
		if err == nil {
			taken++
			continue
		}
		require.ErrorIs(t, err, connectedaccount.ErrStateInvalid)
	}
	assert.Equal(t, 1, taken)
}

func TestConnectStateReconnect(t *testing.T) {
	c := newConnectedAccountsTest(t)
	account, err := c.service.Connect(t.Context(), c.member, alexIdentity, "refresh-1")
	require.NoError(t, err)
	state := newConnectState(c.member, insertConnectingSession(t, c.conn, c.member), account.ID)
	require.NoError(t, saveConnectState(t, c.db, state))

	taken, err := c.db.DeleteConnectState(t.Context(), state.StateHash)
	require.NoError(t, err)

	assert.Equal(t, account.ID, taken.AccountID)
}

// Signing out, or disconnecting the account a state reconnects, ends the
// flow: the state goes with the row it names.
func TestConnectStateDeletedWithItsRows(t *testing.T) {
	// Given a state for a new account in one session, and one reconnecting
	// an account in another.
	c := newConnectedAccountsTest(t)
	session := insertConnectingSession(t, c.conn, c.member)
	account, err := c.service.Connect(t.Context(), c.member, alexIdentity, "refresh-1")
	require.NoError(t, err)
	require.NoError(t, saveConnectState(t, c.db, newConnectState(c.member, session, 0)))
	require.NoError(t, saveConnectState(t, c.db, newConnectState(c.member, insertConnectingSession(t, c.conn, c.member), account.ID)))

	// When the first session signs out and the account is disconnected.
	_, err = c.conn.Exec(t.Context(), "DELETE FROM sessions WHERE id = $1", session)
	require.NoError(t, err)
	_, err = c.service.Disconnect(t.Context(), c.member, account.ID)
	require.NoError(t, err)

	// Then both states are gone.
	assert.Equal(t, int64(0), countConnectStates(t, c.conn))
}

// The start sees when each of the user's states was created, and no other
// user's.
func TestConnectStateTimes(t *testing.T) {
	db, conn := setupStorage(t)
	member := insertConnectingMember(t, conn, "member@example.com")
	other := insertConnectingMember(t, conn, "other@example.com")
	session := insertConnectingSession(t, conn, member)
	first := newConnectState(member, session, 0)
	require.NoError(t, saveConnectState(t, db, first))
	require.NoError(t, saveConnectState(t, db, newConnectState(other, insertConnectingSession(t, conn, other), 0)))

	var seen []time.Time
	err := db.InsertConnectState(t.Context(), member, func(started []time.Time) (connectedaccount.ConnectState, error) {
		seen = started
		return newConnectState(member, session, 0), nil
	})
	require.NoError(t, err)

	require.Len(t, seen, 1, "only the member's own states")
	assert.True(t, first.CreatedAt.Equal(seen[0]))
}

// Starts at once are counted one after another: with room for one, one is
// saved.
func TestConcurrentStarts(t *testing.T) {
	db, conn := setupStorage(t)
	member := insertConnectingMember(t, conn, "member@example.com")
	session := insertConnectingSession(t, conn, member)

	var wg sync.WaitGroup
	refused := errors.New("no room")
	errs := make([]error, 5)
	for i := range errs {
		wg.Go(func() {
			errs[i] = db.InsertConnectState(t.Context(), member, func(started []time.Time) (connectedaccount.ConnectState, error) {
				if len(started) > 0 {
					return connectedaccount.ConnectState{}, refused
				}
				return newConnectState(member, session, 0), nil
			})
		})
	}
	wg.Wait()

	saved := 0
	for _, err := range errs {
		if err == nil {
			saved++
			continue
		}
		require.ErrorIs(t, err, refused)
	}
	assert.Equal(t, 1, saved)
	assert.Equal(t, int64(1), countConnectStates(t, conn))
}

// A deactivated or missing member starts nothing.
func TestConnectStateDeactivated(t *testing.T) {
	db, conn := setupStorage(t)
	member := insertConnectingMember(t, conn, "member@example.com")
	session := insertConnectingSession(t, conn, member)
	_, err := conn.Exec(t.Context(), "UPDATE users SET deactivated_at = now() WHERE id = $1", member)
	require.NoError(t, err)

	for _, userID := range []int64{member, missingUserID} {
		err := db.InsertConnectState(t.Context(), userID, func([]time.Time) (connectedaccount.ConnectState, error) {
			return newConnectState(member, session, 0), nil
		})

		require.ErrorIs(t, err, connectedaccount.ErrDeactivated)
	}
	assert.Equal(t, int64(0), countConnectStates(t, conn))
}

// The cleanup deletes states that expired by its cutoff, up to its limit.
func TestConnectStateCleanup(t *testing.T) {
	db, conn := setupStorage(t)
	member := insertConnectingMember(t, conn, "member@example.com")
	session := insertConnectingSession(t, conn, member)
	cutoff := time.Now().UTC().Truncate(time.Microsecond)
	for _, expiresAt := range []time.Time{cutoff.Add(-2 * time.Hour), cutoff.Add(-time.Hour), cutoff, cutoff.Add(time.Second)} {
		state := newConnectState(member, session, 0)
		state.ExpiresAt = expiresAt
		require.NoError(t, saveConnectState(t, db, state))
	}

	deleted, err := db.DeleteExpiredConnectStates(t.Context(), cutoff, 2)
	require.NoError(t, err)
	assert.Equal(t, 2, deleted)

	deleted, err = db.DeleteExpiredConnectStates(t.Context(), cutoff, 2)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted, "the state expiring at the cutoff")
	assert.Equal(t, int64(1), countConnectStates(t, conn))
}
