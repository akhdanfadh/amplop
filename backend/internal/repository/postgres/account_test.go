package postgres

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
)

func TestReadCredentials(t *testing.T) {
	db, _ := setupStorage(t)
	user, err := db.UpdateSetup(t.Context(), func(account.SetupState) (account.NewAdmin, error) {
		return account.NewAdmin{Email: "Admin@example.com", PasswordHash: setupTestHash}, nil
	})
	require.NoError(t, err)

	row, err := db.ReadCredentials(t.Context(), "ADMIN@EXAMPLE.COM")

	require.NoError(t, err)
	assert.Equal(t, account.Credentials{User: user, PasswordHash: setupTestHash}, row)
	row, err = db.ReadCredentials(t.Context(), "missing@example.com")
	require.ErrorIs(t, err, account.ErrUserNotFound)
	assert.Zero(t, row)
}

func TestAccountAuthentication(t *testing.T) {
	db, _ := setupStorage(t)
	service := account.NewService(db, setupTestToken)
	const password = "a long sign-in password"
	user, err := service.Setup(t.Context(), account.SetupInput{
		Token: setupTestToken, Email: "Admin@Example.COM", Password: password,
	})
	require.NoError(t, err)

	got, err := service.Authenticate(t.Context(), " \tADMIN@example.com\n", password)

	require.NoError(t, err)
	assert.Equal(t, user, got)
	got, err = service.Authenticate(t.Context(), user.Email, "wrong password")
	require.ErrorIs(t, err, account.ErrInvalidCredentials)
	assert.Zero(t, got)
	got, err = service.Authenticate(t.Context(), "missing@example.com", password)
	require.ErrorIs(t, err, account.ErrInvalidCredentials)
	assert.Zero(t, got)
}
