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
	identity  func(context.Context, int64) (IdentityState, error)
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

func (f *currentStorage) ReadSessionIdentity(ctx context.Context, hash [sha256.Size]byte) (IdentityState, error) {
	if f.row == nil || f.row.TokenHash != hash {
		return IdentityState{}, ErrNotFound
	}
	if f.identity != nil {
		return f.identity(ctx, f.row.UserID)
	}
	return IdentityState{User: account.User{ID: f.row.UserID, Role: account.RoleMember}}, nil
}

func TestCurrent(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, time.October, 28, 12, 0, 0, 0, time.UTC)}
	issued := newSession(42, Device{}, clock.Now)
	store := &currentStorage{row: &issued.State}
	user := account.User{ID: 42, Email: "admin@example.com", Role: account.RoleAdmin}
	reads := 0
	store.identity = func(_ context.Context, id int64) (IdentityState, error) {
		reads++
		assert.Equal(t, int64(42), id)
		return IdentityState{User: user}, nil
	}
	service := NewService(store, nil, nil, clock.Now)

	current, err := service.Current(t.Context(), issued.Secret)

	require.NoError(t, err)
	assert.Equal(t, user, current.Identity.User)
	assert.Equal(t, AccessMember, current.Identity.Access, "an admin without TOTP acts as a member")
	assert.True(t, current.Identity.TOTPRequired)
	assert.NotZero(t, current.Session)
	assert.Equal(t, issued.State.ID, current.Session.ID())
	require.Len(t, store.changes, 1)
	assert.Equal(t, Keep, store.changes[0].Action)

	// Later checks must use current user data, including a changed role.
	user.Email, user.Role = "member@example.com", account.RoleMember
	clock.Advance(time.Hour)
	current, err = service.Current(t.Context(), issued.Secret)
	require.NoError(t, err)
	assert.Equal(t, user, current.Identity.User)
	assert.Equal(t, AccessMember, current.Identity.Access)
	assert.False(t, current.Identity.TOTPRequired)
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
			issued := newSession(42, Device{}, clock.Now)
			if lifetime == "idle" {
				clock.Advance(7 * 24 * time.Hour)
			} else {
				clock.Advance(30 * 24 * time.Hour)
				issued.State.LastUsedAt = clock.Now().Add(-time.Hour)
			}
			store := &currentStorage{row: &issued.State}
			store.identity = func(context.Context, int64) (IdentityState, error) {
				t.Error("expired session reached account lookup")
				return IdentityState{}, nil
			}

			identity, err := NewService(store, nil, nil, clock.Now).Current(t.Context(), issued.Secret)

			require.ErrorIs(t, err, ErrUnauthenticated)
			assert.Zero(t, identity)
			assert.Nil(t, store.row, "expiry must commit a deletion")
			require.Len(t, store.changes, 1)
			assert.Equal(t, Delete, store.changes[0].Action)
		})
	}
}

func TestCurrentMissing(t *testing.T) {
	issued := newSession(42, Device{}, time.Now)
	for _, secret := range []string{"", "invalid-secret", strings.Repeat("A", 43)} {
		store := &currentStorage{row: &issued.State}
		store.identity = func(context.Context, int64) (IdentityState, error) {
			t.Error("invalid or unknown secret reached account lookup")
			return IdentityState{}, nil
		}

		identity, err := NewService(store, nil, nil, time.Now).Current(t.Context(), secret)

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
			issued := newSession(42, Device{}, clock.Now)
			store := &currentStorage{row: &issued.State, commitErr: lost}
			switch stage {
			case "read":
				store.readErr = lost
			case "touch commit":
				clock.Advance(time.Hour)
			case "delete commit":
				clock.Advance(7 * 24 * time.Hour)
			}
			store.identity = func(context.Context, int64) (IdentityState, error) {
				t.Error("uncommitted session check reached account lookup")
				return IdentityState{}, nil
			}

			identity, err := NewService(store, nil, nil, clock.Now).Current(t.Context(), issued.Secret)

			require.ErrorIs(t, err, lost)
			assert.Zero(t, identity)
			assert.Empty(t, store.changes)
			assert.Equal(t, &issued.State, store.row)
		})
	}
}

func TestCurrentIdentityErrors(t *testing.T) {
	lost := errors.New("account storage unavailable")
	for _, c := range []struct {
		name string
		user account.User
		err  error
		want error
	}{
		{"revoked session", account.User{}, ErrNotFound, ErrUnauthenticated},
		{"lookup failure", account.User{}, lost, lost},
		{"unknown role", account.User{ID: 42, Role: "unknown"}, nil, errInvalidRole},
	} {
		t.Run(c.name, func(t *testing.T) {
			issued := newSession(42, Device{}, time.Now)
			store := &currentStorage{row: &issued.State}
			store.identity = func(context.Context, int64) (IdentityState, error) {
				return IdentityState{User: c.user}, c.err
			}

			identity, err := NewService(store, nil, nil, time.Now).Current(t.Context(), issued.Secret)

			require.ErrorIs(t, err, c.want)
			assert.Zero(t, identity)
		})
	}
}

func TestCurrentAccess(t *testing.T) {
	for _, c := range []struct {
		name      string
		role      account.Role
		confirmed bool
		verified  bool
		access    Access
		required  bool
	}{
		{"admin off", account.RoleAdmin, false, false, AccessMember, true},
		{"admin on", account.RoleAdmin, true, true, AccessAdmin, false},
		{"member off", account.RoleMember, false, true, AccessMember, false},
		{"member on", account.RoleMember, true, false, AccessMember, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			issued := newSession(42, Device{}, time.Now)
			store := &currentStorage{row: &issued.State, identity: func(context.Context, int64) (IdentityState, error) {
				return IdentityState{User: account.User{ID: 42, Role: c.role}, TOTPConfirmed: c.confirmed, EmailVerified: c.verified}, nil
			}}

			current, err := NewService(store, nil, nil, time.Now).Current(t.Context(), issued.Secret)

			require.NoError(t, err)
			assert.Equal(t, c.access, current.Identity.Access)
			assert.Equal(t, c.required, current.Identity.TOTPRequired)
			assert.Equal(t, c.verified, current.Identity.EmailVerified, "email verification does not change access")
		})
	}
}
