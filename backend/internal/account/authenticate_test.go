package account

import (
	"context"
	"errors"
	"strings"
	"testing"

	"inbok/internal/emailaddress"
	"inbok/internal/passwordhash"

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
	user := User{ID: 42, Email: "admin@example.com"}
	storage := &credentialStorage{credentials: Credentials{User: user, PasswordHash: passwordhash.Hash(password)}}
	service := NewService(storage, "")

	got, err := service.Authenticate(t.Context(), " \tADMIN@Example.COM\t", password)

	require.NoError(t, err)
	assert.Equal(t, storage.credentials, got)
	assert.Equal(t, "admin@example.com", storage.email)

	got, err = service.Authenticate(t.Context(), user.Email, "a long sign-in password")
	require.ErrorIs(t, err, ErrInvalidCredentials)
	assert.Zero(t, got, "password whitespace is significant")
}

func TestAuthenticateRejected(t *testing.T) {
	user := User{ID: 42, Email: "admin@example.com"}
	hash := passwordhash.Hash("the correct password")
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
		{"damaged hash", Credentials{PasswordHash: "damaged"}, nil, passwordhash.ErrInvalidHash},
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

func TestAuthenticateInvalidEmail(t *testing.T) {
	storage := &credentialStorage{}

	got, err := NewService(storage, "").Authenticate(t.Context(), "admin.example.com", "password")

	require.ErrorIs(t, err, emailaddress.ErrInvalid)
	assert.Zero(t, got)
	assert.Empty(t, storage.email, "storage is not read")
}

func TestDummyPasswordHash(t *testing.T) {
	// The dummy must keep pace with the work settings used for new passwords.
	dummy := strings.Split(dummyPasswordHash, "$")
	current := strings.Split(passwordhash.Hash("inbok dummy password"), "$")
	require.Len(t, dummy, 6)
	require.Len(t, current, 6)
	assert.Equal(t, current[:4], dummy[:4])
	assert.Len(t, dummy[4], len(current[4]))
	assert.Len(t, dummy[5], len(current[5]))

	match, err := passwordhash.Verify("inbok dummy password", dummyPasswordHash)
	require.NoError(t, err)
	assert.True(t, match)
}
