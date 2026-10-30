package session

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type signOutStorage struct {
	*currentStorage
	err error
}

func (f *signOutStorage) DeleteSession(_ context.Context, hash [sha256.Size]byte) error {
	if f.err != nil {
		return f.err
	}
	if f.row != nil && f.row.TokenHash == hash {
		f.row = nil
	}
	return nil
}

func TestSignOut(t *testing.T) {
	issued := newSession(42, time.Now)
	other := newSession(42, time.Now)
	store := &signOutStorage{currentStorage: &currentStorage{row: &issued.State}}
	service := NewService(store, nil, nil, time.Now)
	current, err := service.Current(t.Context(), issued.Secret)
	require.NoError(t, err)
	otherStore := &signOutStorage{currentStorage: &currentStorage{row: &other.State}}
	otherCurrent, err := NewService(otherStore, nil, nil, time.Now).Current(t.Context(), other.Secret)
	require.NoError(t, err)

	// Signing out another session must leave this session usable.
	require.NoError(t, service.SignOut(t.Context(), otherCurrent.Session))
	_, err = service.Current(t.Context(), issued.Secret)
	require.NoError(t, err)

	require.NoError(t, service.SignOut(t.Context(), current.Session))
	_, err = service.Current(t.Context(), issued.Secret)
	require.ErrorIs(t, err, ErrUnauthenticated)
	require.NoError(t, service.SignOut(t.Context(), current.Session))
}

func TestSignOutMissingReference(t *testing.T) {
	service := NewService(nil, nil, nil, time.Now)

	err := service.SignOut(t.Context(), Reference{})

	require.ErrorIs(t, err, ErrUnauthenticated)
}

func TestSignOutStorageFailure(t *testing.T) {
	issued := newSession(42, time.Now)
	lost := errors.New("session storage unavailable")
	store := &signOutStorage{currentStorage: &currentStorage{row: &issued.State}, err: lost}
	service := NewService(store, nil, nil, time.Now)
	current, err := service.Current(t.Context(), issued.Secret)
	require.NoError(t, err)

	err = service.SignOut(t.Context(), current.Session)

	require.ErrorIs(t, err, lost)
	assert.Equal(t, &issued.State, store.row)
}
