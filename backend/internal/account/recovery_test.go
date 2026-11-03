package account

import (
	"cmp"
	"context"
	"errors"
	"testing"

	"inbok/internal/emailaddress"
	"inbok/internal/passwordhash"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recoveryStorage struct {
	user     User
	email    string
	hash     string
	locked   *User
	readErr  error
	writeErr error
}

func (f *recoveryStorage) ReadCredentials(_ context.Context, email string) (Credentials, error) {
	f.email = email
	return Credentials{User: f.user}, f.readErr
}

func (f *recoveryStorage) UpdateRecovery(_ context.Context, _ int64, update func(User) (string, error)) (RecoveryResult, error) {
	user := f.user
	if f.locked != nil {
		user = *f.locked
	}
	hash, err := update(user)
	if err != nil {
		return RecoveryResult{}, err
	}
	if f.writeErr != nil {
		return RecoveryResult{}, f.writeErr
	}
	f.hash = hash
	return RecoveryResult{Sessions: 2}, nil
}

func TestRecovery(t *testing.T) {
	storage := &recoveryStorage{user: User{ID: 1, Email: "admin@example.com", Role: RoleAdmin}}
	password := "a new recovery password"

	result, err := NewRecovery(storage).Recover(t.Context(), " \tADMIN@Example.COM\t", password)

	require.NoError(t, err)
	assert.Equal(t, "admin@example.com", storage.email)
	assert.Equal(t, int64(2), result.Sessions)
	match, err := passwordhash.Verify(t.Context(), password, storage.hash)
	require.NoError(t, err)
	assert.True(t, match)
}

func TestRecoveryRejected(t *testing.T) {
	lost := errors.New("storage unavailable")
	for _, c := range []struct {
		name, email       string
		password          string
		role              Role
		locked            *User
		readErr, writeErr error
		want              error
	}{
		{name: "invalid email", email: "admin.example.com", want: emailaddress.ErrInvalid},
		{name: "invalid password", role: RoleAdmin, password: "too short", want: ErrPasswordInvalid},
		{name: "missing", readErr: ErrUserNotFound, want: ErrUserNotFound},
		{name: "member", role: RoleMember, want: ErrRecoveryForbidden},
		{name: "unknown role", role: Role("future"), want: ErrRecoveryForbidden},
		{name: "demoted", role: RoleAdmin, locked: &User{Role: RoleMember}, want: ErrRecoveryForbidden},
		{name: "read failure", readErr: lost, want: lost},
		{name: "write failure", role: RoleAdmin, writeErr: lost, want: lost},
	} {
		t.Run(c.name, func(t *testing.T) {
			storage := &recoveryStorage{user: User{ID: 1, Role: c.role}, locked: c.locked, readErr: c.readErr, writeErr: c.writeErr}
			email := cmp.Or(c.email, "admin@example.com")

			password := cmp.Or(c.password, "a new recovery password")

			result, err := NewRecovery(storage).Recover(t.Context(), email, password)

			require.ErrorIs(t, err, c.want)
			assert.Zero(t, result)
			assert.Empty(t, storage.hash)
		})
	}
}

func TestRecoveryAdministrator(t *testing.T) {
	storage := &recoveryStorage{user: User{ID: 1, Email: "admin@example.com", Role: RoleAdmin}}
	service := NewRecovery(storage)

	user, err := service.Administrator(t.Context(), " \tADMIN@Example.COM\t")

	require.NoError(t, err)
	assert.Equal(t, storage.user, user)
	assert.Equal(t, "admin@example.com", storage.email)
	assert.Empty(t, storage.hash)
	storage.user.Role = RoleMember
	_, err = service.Administrator(t.Context(), "admin@example.com")
	require.ErrorIs(t, err, ErrRecoveryForbidden)
	storage.readErr = ErrUserNotFound
	_, err = service.Administrator(t.Context(), "admin@example.com")
	require.ErrorIs(t, err, ErrUserNotFound)
}
