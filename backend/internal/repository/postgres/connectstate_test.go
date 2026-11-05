package postgres

import (
	"crypto/sha256"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/connectedaccount"
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
	return connectedaccount.ConnectState{
		StateHash:    sha256.Sum256([]byte(uuid.New().String())),
		UserID:       userID,
		SessionID:    session,
		AccountID:    accountID,
		CodeVerifier: "verifier-1",
		ExpiresAt:    time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour),
	}
}

func TestConnectStateStorage(t *testing.T) {
	db, conn := setupStorage(t)
	member := insertConnectingMember(t, conn, "member@example.com")
	state := newConnectState(member, insertConnectingSession(t, conn, member), 0)
	require.NoError(t, db.InsertConnectState(t.Context(), state))

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
	require.NoError(t, db.InsertConnectState(t.Context(), state))

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
	require.NoError(t, c.db.InsertConnectState(t.Context(), state))

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
	require.NoError(t, c.db.InsertConnectState(t.Context(), newConnectState(c.member, session, 0)))
	require.NoError(t, c.db.InsertConnectState(t.Context(), newConnectState(c.member, insertConnectingSession(t, c.conn, c.member), account.ID)))

	// When the first session signs out and the account is disconnected.
	_, err = c.conn.Exec(t.Context(), "DELETE FROM sessions WHERE id = $1", session)
	require.NoError(t, err)
	require.NoError(t, c.service.Disconnect(t.Context(), c.member, account.ID))

	// Then both states are gone.
	assert.Equal(t, int64(0), countConnectStates(t, c.conn))
}
