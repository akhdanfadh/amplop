package connectedaccount

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/encryption"
)

func (m *memoryStorage) ReadAccountRemoval(_ context.Context, removalID int64) (Removal, error) {
	stored, ok := m.removals[removalID]
	if !ok {
		return Removal{}, ErrNotFound
	}
	removal := stored.removal
	for _, s := range m.accounts {
		if s.account.Subject == stored.subject {
			removal.Shared = true
		}
	}
	return removal, nil
}

func (m *memoryStorage) DeleteAccountRemoval(_ context.Context, removalID int64) error {
	delete(m.removals, removalID)
	return nil
}

// fakeRevoker records the tokens it revokes and answers with err.
type fakeRevoker struct {
	err     error
	revoked []string
}

func (f *fakeRevoker) Revoke(_ context.Context, refreshToken string) error {
	f.revoked = append(f.revoked, refreshToken)
	return f.err
}

// revokeTest is [member]'s account, deleted with its token kept as the only
// removal, as a disconnect leaves it.
type revokeTest struct {
	removals *Removals
	accounts *Service
	storage  *memoryStorage
	google   *fakeRevoker
	id       int64
}

func newRevokeTest(t *testing.T) *revokeTest {
	t.Helper()
	service, storage, ring := newTestService(t)
	account, err := service.Connect(t.Context(), member, alex, "refresh-1")
	require.NoError(t, err)
	id := storage.keepRemoval(member, account.ID)
	google := &fakeRevoker{}
	return &revokeTest{
		removals: NewRemovals(storage, ring, google),
		accounts: service,
		storage:  storage,
		google:   google,
		id:       id,
	}
}

func TestRevoke(t *testing.T) {
	r := newRevokeTest(t)

	outcome, err := r.removals.Revoke(t.Context(), r.id)

	require.NoError(t, err)
	assert.Equal(t, RevokeRevoked, outcome)
	assert.Equal(t, []string{"refresh-1"}, r.google.revoked)
	assert.Empty(t, r.storage.removals)

	t.Run("again", func(t *testing.T) {
		outcome, err := r.removals.Revoke(t.Context(), r.id)

		require.NoError(t, err)
		assert.Equal(t, RevokeGone, outcome)
		assert.Len(t, r.google.revoked, 1)
	})
}

// Another member's connection of the same Google account shares the grant,
// which is left alone.
func TestRevokeShared(t *testing.T) {
	r := newRevokeTest(t)
	_, err := r.accounts.Connect(t.Context(), otherMember, alex, "refresh-2")
	require.NoError(t, err)

	outcome, err := r.removals.Revoke(t.Context(), r.id)

	require.NoError(t, err)
	assert.Equal(t, RevokeShared, outcome)
	assert.Empty(t, r.google.revoked)
	assert.Empty(t, r.storage.removals)
}

// A token no key opens is dropped without reaching Google.
func TestRevokeUnreadable(t *testing.T) {
	r := newRevokeTest(t)
	other, err := encryption.New("2:AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI=")
	require.NoError(t, err)
	removals := NewRemovals(r.storage, other, r.google)

	outcome, err := removals.Revoke(t.Context(), r.id)

	require.NoError(t, err)
	assert.Equal(t, RevokeUnreadable, outcome)
	assert.Empty(t, r.google.revoked)
	assert.Empty(t, r.storage.removals)
}

// A failure at Google keeps the removal for another try, and discarding
// it then drops it without revoking.
func TestRevokeFails(t *testing.T) {
	r := newRevokeTest(t)
	broken := errors.New("google unavailable")
	r.google.err = broken

	_, err := r.removals.Revoke(t.Context(), r.id)

	require.ErrorIs(t, err, broken)
	assert.Len(t, r.storage.removals, 1)

	t.Run("discard", func(t *testing.T) {
		require.NoError(t, r.removals.Discard(t.Context(), r.id))

		assert.Empty(t, r.storage.removals)
		assert.Len(t, r.google.revoked, 1)
	})
}
