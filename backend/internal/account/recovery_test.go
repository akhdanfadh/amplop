package account

import (
	"context"
	"errors"
	"strings"
	"testing"

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
	storage := &recoveryStorage{user: User{ID: 1, Email: "ADMIN@example.com", Role: RoleAdmin}}
	password := "a new recovery password"

	result, err := NewRecovery(storage).Recover(t.Context(), " \tADMIN@Example.COM\n", password)

	require.NoError(t, err)
	assert.Equal(t, "ADMIN@example.com", storage.email)
	assert.Equal(t, int64(2), result.Sessions)
	match, err := passwordhash.Verify(password, storage.hash)
	require.NoError(t, err)
	assert.True(t, match)
}

func TestRecoveryRejected(t *testing.T) {
	lost := errors.New("storage unavailable")
	for _, c := range []struct {
		name              string
		role              Role
		locked            *User
		readErr, writeErr error
		want              error
	}{
		{name: "missing", readErr: ErrUserNotFound, want: ErrUserNotFound},
		{name: "member", role: RoleMember, want: ErrRecoveryForbidden},
		{name: "unknown role", role: Role("future"), want: ErrRecoveryForbidden},
		{name: "demoted", role: RoleAdmin, locked: &User{Role: RoleMember}, want: ErrRecoveryForbidden},
		{name: "read failure", readErr: lost, want: lost},
		{name: "write failure", role: RoleAdmin, writeErr: lost, want: lost},
	} {
		t.Run(c.name, func(t *testing.T) {
			storage := &recoveryStorage{user: User{ID: 1, Role: c.role}, locked: c.locked, readErr: c.readErr, writeErr: c.writeErr}

			result, err := NewRecovery(storage).Recover(t.Context(), "admin@example.com", "a new recovery password")

			require.ErrorIs(t, err, c.want)
			assert.Zero(t, result)
			assert.Empty(t, storage.hash)
		})
	}
}

func TestRecoveryPasswordBounds(t *testing.T) {
	for _, c := range []struct {
		name, password string
		valid          bool
	}{
		{"empty", "", false},
		{"too short", strings.Repeat("a", 14), false},
		{"minimum", strings.Repeat("a", 15), true},
		{"maximum", strings.Repeat("a", 128), true},
		{"too long", strings.Repeat("a", 129), false},
		{"Unicode too short", strings.Repeat("\u754c", 14), false},
		{"Unicode minimum", strings.Repeat("\u754c", 15), true},
		{"Unicode maximum", strings.Repeat("\u754c", 128), true},
		{"Unicode too long", strings.Repeat("\u754c", 129), false},
		{"spaces preserved", "  a long password  ", true},
		{"invalid UTF-8", strings.Repeat("a", 15) + "\xff", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			storage := &recoveryStorage{user: User{ID: 1, Role: RoleAdmin}}

			result, err := NewRecovery(storage).Recover(t.Context(), "admin@example.com", c.password)

			if !c.valid {
				require.ErrorIs(t, err, ErrPasswordInvalid)
				assert.Zero(t, result)
				assert.Empty(t, storage.hash)
				return
			}
			require.NoError(t, err)
			match, err := passwordhash.Verify(c.password, storage.hash)
			require.NoError(t, err)
			assert.True(t, match)
		})
	}
}
