package postgres

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
)

// Storage matches emails exactly; the account service normalizes them first.
func TestReadCredentials(t *testing.T) {
	db, _ := setupStorage(t)
	user, err := db.UpdateSetup(t.Context(), func(account.SetupState) (account.NewAdmin, error) {
		return account.NewAdmin{Email: "admin@example.com", PasswordHash: setupTestHash}, nil
	})
	require.NoError(t, err)

	row, err := db.ReadCredentials(t.Context(), "admin@example.com")

	require.NoError(t, err)
	assert.Equal(t, account.Credentials{User: user, PasswordHash: setupTestHash}, row)
	for _, email := range []string{"ADMIN@EXAMPLE.COM", "missing@example.com"} {
		row, err = db.ReadCredentials(t.Context(), email)
		require.ErrorIs(t, err, account.ErrUserNotFound, email)
		assert.Zero(t, row)
	}
}

func TestAccountAuthentication(t *testing.T) {
	db, _ := setupStorage(t)
	service := account.NewService(db, setupTestToken)
	const password = "a long sign-in password"
	user, err := service.Setup(t.Context(), account.SetupInput{
		Token: setupTestToken, Email: "Admin@Example.COM", Password: password,
	})
	require.NoError(t, err)

	got, err := service.Authenticate(t.Context(), " \tADMIN@example.com\t", password)

	require.NoError(t, err)
	assert.Equal(t, user, got.User)
	got, err = service.Authenticate(t.Context(), user.Email, "wrong password")
	require.ErrorIs(t, err, account.ErrInvalidCredentials)
	assert.Zero(t, got)
	got, err = service.Authenticate(t.Context(), "missing@example.com", password)
	require.ErrorIs(t, err, account.ErrInvalidCredentials)
	assert.Zero(t, got)
}

func TestReadUser(t *testing.T) {
	db, conn := setupStorage(t)
	accounts := account.NewService(db, setupTestToken)
	user, err := accounts.Setup(t.Context(), account.SetupInput{
		Token: setupTestToken, Email: "Admin@example.com", Password: "a long setup password",
	})
	require.NoError(t, err)

	got, err := accounts.UserByID(t.Context(), user.ID)

	require.NoError(t, err)
	assert.Equal(t, account.User{ID: user.ID, Email: "admin@example.com", Role: account.RoleAdmin}, got)

	// Identity reads must reflect later changes without requiring a new session.
	_, err = conn.Exec(t.Context(), "UPDATE users SET email = $1, role = $2 WHERE id = $3", "member@example.com", account.RoleMember, user.ID)
	require.NoError(t, err)
	got, err = accounts.UserByID(t.Context(), user.ID)
	require.NoError(t, err)
	assert.Equal(t, account.User{ID: user.ID, Email: "member@example.com", Role: account.RoleMember}, got)
	got, err = accounts.UserByID(t.Context(), user.ID+1)
	require.ErrorIs(t, err, account.ErrUserNotFound)
	assert.Zero(t, got)
}
