package registration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cleanupMemory records what [Cleanup] asks storage to delete and answers
// with deleted or err.
type cleanupMemory struct {
	expiredBy time.Time
	limit     int
	deleted   int
	err       error
}

func (m *cleanupMemory) DeleteExpiredRegistrationLinks(_ context.Context, expiredBy time.Time, limit int) (int, error) {
	m.expiredBy, m.limit = expiredBy, limit
	return m.deleted, m.err
}

// One call deletes up to a batch of links that expired [cleanupGrace] or more
// before now, and reports more only when it deleted a full batch.
func TestCleanup(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	cases := map[string]struct {
		deleted int
		more    bool
	}{
		"none":       {0, false},
		"some":       {cleanupBatch - 1, false},
		"full batch": {cleanupBatch, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			storage := &cleanupMemory{deleted: c.deleted}

			deleted, more, err := NewCleanup(storage, func() time.Time { return now }).DeleteExpired(t.Context())
			require.NoError(t, err)

			assert.Equal(t, c.deleted, deleted)
			assert.Equal(t, c.more, more)
			assert.Equal(t, now.Add(-cleanupGrace), storage.expiredBy)
			assert.Equal(t, cleanupBatch, storage.limit)
		})
	}
}

// A storage error reaches the caller unchanged.
func TestCleanupError(t *testing.T) {
	lost := errors.New("connection lost")
	storage := &cleanupMemory{err: lost}

	_, _, err := NewCleanup(storage, time.Now).DeleteExpired(t.Context())

	require.ErrorIs(t, err, lost)
}
