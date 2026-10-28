package session

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
)

type currentStorage struct {
	Storage
	row       *State
	changes   []Change
	readErr   error
	commitErr error
}

func (f *currentStorage) UpdateSession(_ context.Context, hash [sha256.Size]byte, update func(State) (Change, error)) error {
	if f.readErr != nil {
		return f.readErr
	}
	if f.row == nil || f.row.TokenHash != hash {
		return ErrNotFound
	}
	change, err := update(*f.row)
	if err != nil {
		return err
	}
	if f.commitErr != nil {
		return f.commitErr
	}
	f.changes = append(f.changes, change)
	switch change.Action {
	case Keep:
	case Touch:
		f.row.LastUsedAt = change.LastUsedAt
	case Delete:
		f.row = nil
	default:
		panic("unexpected session action")
	}
	return nil
}

func TestCurrent(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, time.October, 28, 12, 0, 0, 0, time.UTC)}
	issued := newSession(42, clock.Now)
	store := &currentStorage{row: &issued.State}
	user := account.User{ID: 42, Email: "admin@example.com", Role: account.RoleAdmin}
	reads := 0
	accounts := signInAccounts{userByID: func(_ context.Context, id int64) (account.User, error) {
		reads++
		assert.Equal(t, int64(42), id)
		return user, nil
	}}
	service := NewService(store, accounts, nil, clock.Now)

	identity, err := service.Current(t.Context(), issued.Secret)

	require.NoError(t, err)
	assert.Equal(t, user, identity.User)
	assert.Equal(t, AccessMember, identity.Access, "an admin without TOTP acts as a member")
	assert.True(t, identity.TOTPRequired)
	require.Len(t, store.changes, 1)
	assert.Equal(t, Keep, store.changes[0].Action)

	// Later checks must use current user data, including a changed role.
	user.Email, user.Role = "member@example.com", account.RoleMember
	clock.Advance(time.Hour)
	identity, err = service.Current(t.Context(), issued.Secret)
	require.NoError(t, err)
	assert.Equal(t, user, identity.User)
	assert.Equal(t, AccessMember, identity.Access)
	assert.False(t, identity.TOTPRequired)
	assert.Equal(t, 2, reads)
	assert.Equal(t, clock.Now(), store.row.LastUsedAt)
	assert.Equal(t, issued.State.CreatedAt.Add(30*24*time.Hour), store.row.ExpiresAt)
	require.Len(t, store.changes, 2)
	assert.Equal(t, Touch, store.changes[1].Action)

	clock.Advance(time.Minute)
	_, err = service.Current(t.Context(), issued.Secret)
	require.NoError(t, err)
	assert.Equal(t, clock.Now().Add(-time.Minute), store.row.LastUsedAt, "do not write again within an hour")
	assert.Equal(t, 3, reads)
}

func TestCurrentExpired(t *testing.T) {
	for _, lifetime := range []string{"idle", "maximum"} {
		t.Run(lifetime, func(t *testing.T) {
			clock := &fakeClock{now: time.Date(2026, time.October, 28, 12, 0, 0, 0, time.UTC)}
			issued := newSession(42, clock.Now)
			if lifetime == "idle" {
				clock.Advance(7 * 24 * time.Hour)
			} else {
				clock.Advance(30 * 24 * time.Hour)
				issued.State.LastUsedAt = clock.Now().Add(-time.Hour)
			}
			store := &currentStorage{row: &issued.State}
			accounts := signInAccounts{userByID: func(context.Context, int64) (account.User, error) {
				t.Error("expired session reached account lookup")
				return account.User{}, nil
			}}

			identity, err := NewService(store, accounts, nil, clock.Now).Current(t.Context(), issued.Secret)

			require.ErrorIs(t, err, ErrUnauthenticated)
			assert.Zero(t, identity)
			assert.Nil(t, store.row, "expiry must commit a deletion")
			require.Len(t, store.changes, 1)
			assert.Equal(t, Delete, store.changes[0].Action)
		})
	}
}

func TestCurrentMissing(t *testing.T) {
	issued := newSession(42, time.Now)
	for _, secret := range []string{"", "invalid-secret", strings.Repeat("A", 43)} {
		store := &currentStorage{row: &issued.State}
		accounts := signInAccounts{userByID: func(context.Context, int64) (account.User, error) {
			t.Error("invalid or unknown secret reached account lookup")
			return account.User{}, nil
		}}

		identity, err := NewService(store, accounts, nil, time.Now).Current(t.Context(), secret)

		require.ErrorIs(t, err, ErrUnauthenticated)
		assert.Zero(t, identity)
		assert.Empty(t, store.changes)
		assert.Equal(t, &issued.State, store.row)
	}
}

func TestCurrentStorageErrors(t *testing.T) {
	lost := errors.New("session storage unavailable")
	for _, stage := range []string{"read", "keep commit", "touch commit", "delete commit"} {
		t.Run(stage, func(t *testing.T) {
			clock := &fakeClock{now: time.Date(2026, time.October, 28, 12, 0, 0, 0, time.UTC)}
			issued := newSession(42, clock.Now)
			store := &currentStorage{row: &issued.State, commitErr: lost}
			switch stage {
			case "read":
				store.readErr = lost
			case "touch commit":
				clock.Advance(time.Hour)
			case "delete commit":
				clock.Advance(7 * 24 * time.Hour)
			}
			accounts := signInAccounts{userByID: func(context.Context, int64) (account.User, error) {
				t.Error("uncommitted session check reached account lookup")
				return account.User{}, nil
			}}

			identity, err := NewService(store, accounts, nil, clock.Now).Current(t.Context(), issued.Secret)

			require.ErrorIs(t, err, lost)
			assert.Zero(t, identity)
			assert.Empty(t, store.changes)
			assert.Equal(t, &issued.State, store.row)
		})
	}
}

func TestCurrentAccountErrors(t *testing.T) {
	lost := errors.New("account storage unavailable")
	for _, c := range []struct {
		name string
		user account.User
		err  error
		want error
	}{
		{"missing user", account.User{}, account.ErrUserNotFound, ErrUnauthenticated},
		{"lookup failure", account.User{}, lost, lost},
		{"unknown role", account.User{ID: 42, Role: "unknown"}, nil, errInvalidRole},
	} {
		t.Run(c.name, func(t *testing.T) {
			issued := newSession(42, time.Now)
			store := &currentStorage{row: &issued.State}
			accounts := signInAccounts{userByID: func(context.Context, int64) (account.User, error) {
				return c.user, c.err
			}}

			identity, err := NewService(store, accounts, nil, time.Now).Current(t.Context(), issued.Secret)

			require.ErrorIs(t, err, c.want)
			assert.Zero(t, identity)
		})
	}
}
