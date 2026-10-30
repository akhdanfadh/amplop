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
