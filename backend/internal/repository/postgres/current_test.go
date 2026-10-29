package postgres

import (
	"crypto/sha256"
	"encoding/base64"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/session"
)

func TestCurrentPersistence(t *testing.T) {
	db, conn := setupStorage(t)
	accounts := account.NewService(db, setupTestToken)
	const password = "a long sign-in password"
	user, err := accounts.Setup(t.Context(), account.SetupInput{
		Token: setupTestToken, Email: "Admin@example.com", Password: password,
	})
	require.NoError(t, err)
	now := time.Date(2026, time.October, 28, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	service := session.NewService(db, accounts, session.NewThrottle(db, clock), clock)
	issued, err := service.StartSignIn(t.Context(), user.Email, password, netip.MustParseAddr("192.0.2.1"))
	require.NoError(t, err)
	raw, err := base64.RawURLEncoding.Strict().DecodeString(issued.Secret)
	require.NoError(t, err)
	tokenHash := sha256.Sum256(raw)

	current, err := service.Current(t.Context(), issued.Secret)

	require.NoError(t, err)
	assert.Equal(t, user, current.Identity.User)
	assert.Equal(t, session.AccessMember, current.Identity.Access)
	assert.True(t, current.Identity.TOTPRequired)
	assert.Equal(t, now, readSession(t, db, tokenHash).LastUsedAt)

	_, err = conn.Exec(t.Context(), "UPDATE users SET email = $1, role = $2 WHERE id = $3", "member@example.com", account.RoleMember, user.ID)
	require.NoError(t, err)
	now = now.Add(time.Hour)
	current, err = service.Current(t.Context(), issued.Secret)
	require.NoError(t, err)
	assert.Equal(t, account.User{ID: user.ID, Email: "member@example.com", Role: account.RoleMember}, current.Identity.User)
	assert.Equal(t, session.AccessMember, current.Identity.Access)
	assert.False(t, current.Identity.TOTPRequired)
	state := readSession(t, db, tokenHash)
	assert.Equal(t, now, state.LastUsedAt)
	assert.Equal(t, issued.ExpiresAt, state.ExpiresAt)

	now = now.Add(time.Minute)
	_, err = service.Current(t.Context(), issued.Secret)
	require.NoError(t, err)
	assert.Equal(t, state.LastUsedAt, readSession(t, db, tokenHash).LastUsedAt)

	now = state.LastUsedAt.Add(7 * 24 * time.Hour)
	current, err = service.Current(t.Context(), issued.Secret)
	require.ErrorIs(t, err, session.ErrUnauthenticated)
	assert.Zero(t, current)
	err = db.UpdateSession(t.Context(), tokenHash, func(session.State) (session.Change, error) {
		t.Error("expired session row survived its check")
		return session.Change{Action: session.Keep}, nil
	})
	require.ErrorIs(t, err, session.ErrNotFound)
}

func TestCurrentDeleteFailure(t *testing.T) {
	db, state := sessionStorage(t)
	// This base64url secret encodes 32 zero bytes.
	const secret = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	const secretBytes = 32
	state.TokenHash = sha256.Sum256(make([]byte, secretBytes))
	require.NoError(t, db.InsertSession(t.Context(), state))
	_, err := db.pool.Exec(t.Context(), `
		CREATE FUNCTION reject_session_delete() RETURNS trigger LANGUAGE plpgsql AS
		$$ BEGIN RAISE EXCEPTION 'forced session deletion failure'; END $$;
		CREATE CONSTRAINT TRIGGER reject_session_delete AFTER DELETE ON sessions
		DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_session_delete();`)
	require.NoError(t, err)
	clock := func() time.Time { return state.LastUsedAt.Add(7 * 24 * time.Hour) }
	service := session.NewService(db, account.NewService(db, ""), nil, clock)

	identity, err := service.Current(t.Context(), secret)

	require.ErrorContains(t, err, "forced session deletion failure")
	assert.Zero(t, identity)
	assert.Equal(t, state, readSession(t, db, state.TokenHash), "failed commit keeps the row available for a later deletion")

	_, err = db.pool.Exec(t.Context(), "DROP TRIGGER reject_session_delete ON sessions")
	require.NoError(t, err)
	identity, err = service.Current(t.Context(), secret)
	require.ErrorIs(t, err, session.ErrUnauthenticated)
	assert.Zero(t, identity)
}
