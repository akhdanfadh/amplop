package postgres

import (
	"crypto/sha256"
	"encoding/base64"
	"net/netip"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/session"
)

func TestStartSignInPersistence(t *testing.T) {
	db, conn := setupStorage(t)
	accounts := account.NewService(db, setupTestToken)
	const password = "a long sign-in password"
	user, err := accounts.Setup(t.Context(), account.SetupInput{
		Token: setupTestToken, Email: "Admin@Example.COM", Password: password,
	})
	require.NoError(t, err)
	service := session.NewService(db, accounts, session.NewThrottle(db, time.Now), time.Now)
	addr := netip.MustParseAddr("192.0.2.1")
	for range 4 {
		issued, err := service.StartSignIn(t.Context(), "admin@example.com", "wrong password", session.Device{Addr: addr})
		require.ErrorIs(t, err, account.ErrInvalidCredentials)
		assert.Zero(t, issued)
	}

	device := session.Device{UserAgent: "Mozilla/5.0 Firefox/131.0", Addr: addr}
	issued, err := service.StartSignIn(t.Context(), " \tADMIN@example.com\n", password, device)

	require.NoError(t, err)
	assert.Equal(t, session.SignInStepComplete, issued.Step)
	raw, err := base64.RawURLEncoding.Strict().DecodeString(issued.Secret)
	require.NoError(t, err)
	stored := readSession(t, db, sha256.Sum256(raw))
	assert.Equal(t, user.ID, stored.UserID)
	assert.Equal(t, device, stored.Device)
	assert.Equal(t, stored.CreatedAt, stored.LastUsedAt)
	assert.WithinDuration(t, issued.ExpiresAt, stored.ExpiresAt, time.Microsecond)
	var count int64
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM sign_in_attempts").Scan(&count))
	assert.Equal(t, int64(4), count, "success keeps earlier failures")

	_, err = service.StartSignIn(t.Context(), "admin@example.com", "wrong password", session.Device{Addr: addr})
	require.ErrorIs(t, err, account.ErrInvalidCredentials)
	issued, err = service.StartSignIn(t.Context(), "admin@example.com", password, session.Device{Addr: addr})
	require.ErrorIs(t, err, session.ErrThrottled)
	assert.Zero(t, issued)
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM sessions").Scan(&count))
	assert.Equal(t, int64(1), count, "failures cannot create sessions")
}

func TestStartSignInExpiredSessions(t *testing.T) {
	f := newUserSessions(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	expiry := session.Expiry{ExpiresBy: now, LastUsedBy: now.Add(-7 * 24 * time.Hour)}
	// f.expired is long idle; these rows sit on each side of both limits.
	row := func(name string, userID int64, lastUsed, expires time.Time) session.State {
		return f.insert(t, session.State{
			TokenHash: sha256.Sum256([]byte(name)), UserID: userID,
			CreatedAt: lastUsed, LastUsedAt: lastUsed, ExpiresAt: expires,
		})
	}
	live := now.Add(time.Hour)
	row("idle at limit", f.current.UserID, expiry.LastUsedBy, live)
	idleUnder := row("idle under limit", f.current.UserID, expiry.LastUsedBy.Add(time.Microsecond), live)
	row("lifetime at limit", f.current.UserID, now, now)
	lifetimeUnder := row("lifetime under limit", f.current.UserID, now, now.Add(time.Microsecond))
	foreignExpired := row("foreign expired", f.foreign.UserID, expiry.LastUsedBy, live)
	issued := f.current
	issued.TokenHash = sha256.Sum256([]byte("issued"))
	issued.CreatedAt, issued.LastUsedAt = now, now

	err := f.db.StartSignIn(t.Context(), f.current.UserID, func(session.SignInState) (session.SignInChange, error) {
		return session.SignInChange{Step: session.SignInStepComplete, Session: issued, Expiry: expiry}, nil
	})

	require.NoError(t, err)
	require.NoError(t, f.db.pool.QueryRow(t.Context(), "SELECT id FROM sessions WHERE token_hash = $1", issued.TokenHash[:]).Scan(&issued.ID))
	assert.ElementsMatch(t, []uuid.UUID{f.current.ID, f.other.ID, idleUnder.ID, lifetimeUnder.ID, issued.ID}, f.ids(t, f.current.UserID),
		"only rows at or past a limit are deleted")
	assert.ElementsMatch(t, []uuid.UUID{f.foreign.ID, foreignExpired.ID}, f.ids(t, f.foreign.UserID),
		"another user's expired session is untouched")
}
