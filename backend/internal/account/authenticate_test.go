package account

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type credentialStorage struct {
	fakeStorage
	credentials Credentials
	err         error
	email       string
}

func (f *credentialStorage) ReadCredentials(_ context.Context, email string) (Credentials, error) {
	f.email = email
	return f.credentials, f.err
}

func TestAuthenticate(t *testing.T) {
	const password = "a long sign-in password "
	user := User{ID: 42, Email: "Admin@example.com"}
	storage := &credentialStorage{credentials: Credentials{User: user, PasswordHash: hashPassword(password)}}
	service := NewService(storage, "")

	got, err := service.Authenticate(t.Context(), " \tADMIN@Example.COM\n", password)

	require.NoError(t, err)
	assert.Equal(t, user, got)
	assert.Equal(t, "ADMIN@example.com", storage.email)

	got, err = service.Authenticate(t.Context(), user.Email, "a long sign-in password")
	require.ErrorIs(t, err, ErrInvalidCredentials)
	assert.Zero(t, got, "password whitespace is significant")
}

func TestAuthenticateRejected(t *testing.T) {
	user := User{ID: 42, Email: "admin@example.com"}
	hash := hashPassword("the correct password")
	cases := []struct {
		name     string
		row      Credentials
		err      error
		password string
	}{
		{"wrong password", Credentials{User: user, PasswordHash: hash}, nil, "wrong password"},
		{"unknown email", Credentials{}, ErrUserNotFound, "wrong password"},
		{"unknown email with matching dummy", Credentials{}, ErrUserNotFound, "inbok dummy password"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			storage := &credentialStorage{credentials: c.row, err: c.err}

			got, err := NewService(storage, "").Authenticate(t.Context(), user.Email, c.password)

			require.ErrorIs(t, err, ErrInvalidCredentials)
			assert.Zero(t, got)
		})
	}
}

func TestAuthenticateErrors(t *testing.T) {
	lost := errors.New("connection lost")
	cases := []struct {
		name string
		row  Credentials
		err  error
		want error
	}{
		{"storage", Credentials{}, lost, lost},
		{"damaged hash", Credentials{PasswordHash: "damaged"}, nil, errInvalidPasswordHash},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			storage := &credentialStorage{credentials: c.row, err: c.err}

			got, err := NewService(storage, "").Authenticate(t.Context(), "admin@example.com", "password")

			require.ErrorIs(t, err, c.want)
			assert.Zero(t, got)
		})
	}
}

func TestDummyPasswordHash(t *testing.T) {
	hash, err := decodePasswordHash(dummyPasswordHash)
	require.NoError(t, err)
	assert.Equal(t, uint32(passwordMemory), hash.memory)
	assert.Equal(t, uint32(passwordIterations), hash.iterations)
	assert.Equal(t, uint8(passwordParallelism), hash.parallelism)
	assert.Len(t, hash.salt, passwordSaltLength)
	assert.Len(t, hash.key, passwordKeyLength)
	match, err := verifyPassword("inbok dummy password", dummyPasswordHash)
	require.NoError(t, err)
	assert.True(t, match)
}
