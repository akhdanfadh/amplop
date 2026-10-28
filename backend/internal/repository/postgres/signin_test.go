package postgres

import (
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/session"
)

func TestSignInPersistence(t *testing.T) {
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
		issued, err := service.SignIn(t.Context(), "admin@example.com", "wrong password", addr)
		require.ErrorIs(t, err, account.ErrInvalidCredentials)
		assert.Zero(t, issued)
	}

	issued, err := service.SignIn(t.Context(), " \tADMIN@example.com\n", password, addr)

	require.NoError(t, err)
	assert.Equal(t, user.ID, issued.State.UserID)
	stored := readSession(t, db, issued.State.TokenHash)
	assert.Equal(t, user.ID, stored.UserID)
	assert.WithinDuration(t, issued.State.CreatedAt, stored.CreatedAt, time.Microsecond)
	assert.WithinDuration(t, issued.State.ExpiresAt, stored.ExpiresAt, time.Microsecond)
	var count int64
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM sign_in_attempts").Scan(&count))
	assert.Equal(t, int64(4), count, "success keeps earlier failures")

	_, err = service.SignIn(t.Context(), "admin@example.com", "wrong password", addr)
	require.ErrorIs(t, err, account.ErrInvalidCredentials)
	issued, err = service.SignIn(t.Context(), "admin@example.com", password, addr)
	require.ErrorIs(t, err, session.ErrThrottled)
	assert.Zero(t, issued)
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM sessions").Scan(&count))
	assert.Equal(t, int64(1), count, "failures cannot create sessions")
}
