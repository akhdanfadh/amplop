package connectedaccount

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCleanupStorage answers every call with deleted and err, and records
// what it was asked for.
type fakeCleanupStorage struct {
	deleted int
	err     error

	expiredBy time.Time
	limit     int
}

func (f *fakeCleanupStorage) DeleteExpiredConnectStates(_ context.Context, expiredBy time.Time, limit int) (int, error) {
	f.expiredBy, f.limit = expiredBy, limit
	return f.deleted, f.err
}

func TestStateCleanup(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name    string
		deleted int
		more    bool
	}{
		{"short batch", 3, false},
		{"full batch", cleanupBatch, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			storage := &fakeCleanupStorage{deleted: c.deleted}

			deleted, more, err := NewStateCleanup(storage, func() time.Time { return now }).DeleteExpired(t.Context())

			require.NoError(t, err)
			assert.Equal(t, c.deleted, deleted)
			assert.Equal(t, c.more, more)
			assert.Equal(t, now.Add(-cleanupGrace), storage.expiredBy)
			assert.Equal(t, cleanupBatch, storage.limit)
		})
	}
}

func TestStateCleanupFails(t *testing.T) {
	broken := errors.New("database unavailable")
	storage := &fakeCleanupStorage{err: broken}

	_, more, err := NewStateCleanup(storage, time.Now).DeleteExpired(t.Context())

	require.ErrorIs(t, err, broken)
	assert.False(t, more)
}
