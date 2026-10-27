package account

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSetupToken = "test-only-setup-token-for-account-tests"

type fakeStorage struct {
	state       SetupState
	lockedState *SetupState // state another request committed before our lock
	saved       *NewAdmin
	readErr     error
	writeErr    error
}

func (f *fakeStorage) ReadSetup(context.Context) (SetupState, error) {
	return f.state, f.readErr
}

func (f *fakeStorage) UpdateSetup(_ context.Context, update func(SetupState) (NewAdmin, error)) (User, error) {
	if f.lockedState != nil {
		f.state = *f.lockedState
	}

	admin, err := update(f.state)
	if err != nil {
		return User{}, err
	}
	if f.writeErr != nil {
		return User{}, f.writeErr
	}

	f.saved = &admin
	f.state.AdminExists = true
	return User{ID: 1, Email: admin.Email}, nil
}

func TestSetupStatus(t *testing.T) {
	cases := []struct {
		name  string
		token string
		state SetupState
		want  SetupStatus
	}{
		{"unconfigured", "", SetupState{}, SetupUnconfigured},
		{"open", testSetupToken, SetupState{}, SetupOpen},
		{"done with token", testSetupToken, SetupState{AdminExists: true}, SetupDone},
		{"done without token", "", SetupState{AdminExists: true}, SetupDone},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			storage := &fakeStorage{state: c.state}

			status, err := NewService(storage, c.token).SetupStatus(t.Context())

			require.NoError(t, err)
			assert.Equal(t, c.want, status)
		})
	}
}

func TestSetup(t *testing.T) {
	storage := &fakeStorage{}
	service := NewService(storage, testSetupToken)
	input := SetupInput{
		Token:    testSetupToken,
		Email:    " \tADMIN@Example.COM \n",
		Password: "a long setup password",
	}

	user, err := service.Setup(t.Context(), input)

	require.NoError(t, err)
	assert.Equal(t, User{ID: 1, Email: "ADMIN@example.com"}, user)
	require.NotNil(t, storage.saved)
	assert.Equal(t, user.Email, storage.saved.Email)

	match, err := verifyPassword(input.Password, storage.saved.PasswordHash)
	require.NoError(t, err)
	assert.True(t, match)

	status, err := service.SetupStatus(t.Context())
	require.NoError(t, err)
	assert.Equal(t, SetupDone, status)
}

func TestSetupEmail(t *testing.T) {
	cases := []struct {
		name, input, want string
	}{
		{"mixed case", "Mixed.Case+Tag@Example.COM", "Mixed.Case+Tag@example.com"},
		{"outer whitespace", " \tAdmin@EXAMPLE.COM\n", "Admin@example.com"},
		{"already normalized", "admin@example.com", "admin@example.com"},
		{"quoted at sign", `"Ab@CD"@EXAMPLE.COM`, `"Ab@CD"@example.com`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			storage := &fakeStorage{}
			input := SetupInput{Token: testSetupToken, Email: c.input, Password: "a long setup password"}

			user, err := NewService(storage, testSetupToken).Setup(t.Context(), input)

			require.NoError(t, err)
			assert.Equal(t, c.want, user.Email)
			require.NotNil(t, storage.saved)
			assert.Equal(t, c.want, storage.saved.Email)
		})
	}
}

func TestSetupClaimedWhileHashing(t *testing.T) {
	storage := &fakeStorage{lockedState: &SetupState{AdminExists: true}}
	service := NewService(storage, testSetupToken)

	user, err := service.Setup(t.Context(), SetupInput{
		Token: testSetupToken, Email: "admin@example.com", Password: "a long setup password",
	})

	require.ErrorIs(t, err, ErrSetupDone)
	assert.Zero(t, user)
	assert.Nil(t, storage.saved)
}

func TestSetupRejected(t *testing.T) {
	cases := []struct {
		name       string
		configured string
		submitted  string
		state      SetupState
		err        error
	}{
		{"done before wrong token", testSetupToken, "wrong", SetupState{AdminExists: true}, ErrSetupDone},
		{"done before unconfigured", "", "wrong", SetupState{AdminExists: true}, ErrSetupDone},
		{"unconfigured", "", testSetupToken, SetupState{}, ErrSetupUnconfigured},
		{"wrong token", testSetupToken, strings.Repeat("x", len(testSetupToken)), SetupState{}, ErrSetupTokenInvalid},
		{"empty token", testSetupToken, "", SetupState{}, ErrSetupTokenInvalid},
		{"shorter token", testSetupToken, testSetupToken[:len(testSetupToken)-1], SetupState{}, ErrSetupTokenInvalid},
		{"longer token", testSetupToken, testSetupToken + "x", SetupState{}, ErrSetupTokenInvalid},
		{"token has extra space", testSetupToken, testSetupToken + " ", SetupState{}, ErrSetupTokenInvalid},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			storage := &fakeStorage{state: c.state}

			user, err := NewService(storage, c.configured).Setup(t.Context(), SetupInput{Token: c.submitted})

			require.ErrorIs(t, err, c.err)
			assert.Zero(t, user)
			assert.Nil(t, storage.saved)
		})
	}
}

func TestSetupStorageErrors(t *testing.T) {
	lost := errors.New("connection lost")
	storage := &fakeStorage{readErr: lost}
	service := NewService(storage, testSetupToken)
	input := SetupInput{Token: testSetupToken, Email: "admin@example.com", Password: "a long setup password"}

	status, err := service.SetupStatus(t.Context())
	require.ErrorIs(t, err, lost)
	assert.Empty(t, status)

	user, err := service.Setup(t.Context(), input)
	require.ErrorIs(t, err, lost)
	assert.Zero(t, user)
	assert.Nil(t, storage.saved)

	storage.readErr = nil
	storage.writeErr = lost
	user, err = service.Setup(t.Context(), input)
	require.ErrorIs(t, err, lost)
	assert.Zero(t, user)
	assert.Nil(t, storage.saved)
}
