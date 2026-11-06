package passwordreset

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cleanupMemory records what a cleanup asks storage to delete and answers
// with deleted or err.
type cleanupMemory struct {
	links   bool // links were asked for, not attempts
	cutoff  time.Time
	limit   int
	deleted int
	err     error
}

func (m *cleanupMemory) DeleteExpiredPasswordResetLinks(_ context.Context, expiredBy time.Time, limit int) (int, error) {
	m.links, m.cutoff, m.limit = true, expiredBy, limit
	return m.deleted, m.err
}

func (m *cleanupMemory) DeleteOldPasswordResetAttempts(_ context.Context, since time.Time, limit int) (int, error) {
	m.links, m.cutoff, m.limit = false, since, limit
	return m.deleted, m.err
}

// cleanupNow is the clock of these tests.
var cleanupNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// Each cleanup deletes up to a batch of its rows past their cutoff by
// [cleanupGrace], and reports more only when it deleted a full batch: links
// past their expiry, attempts past the longest limit's window.
func TestCleanup(t *testing.T) {
	type deleteExpired func(cleanupStorage) (int, bool, error)
	kinds := map[string]struct {
		run    deleteExpired
		links  bool
		cutoff time.Time
	}{
		"links": {
			run: func(s cleanupStorage) (int, bool, error) {
				return NewCleanup(s, func() time.Time { return cleanupNow }).DeleteExpired(t.Context())
			},
			links: true, cutoff: cleanupNow.Add(-cleanupGrace),
		},
		"attempts": {
			run: func(s cleanupStorage) (int, bool, error) {
				return NewAttemptCleanup(s, func() time.Time { return cleanupNow }).DeleteExpired(t.Context())
			},
			cutoff: cleanupNow.Add(-24*time.Hour - cleanupGrace),
		},
	}
	batches := map[string]struct {
		deleted int
		more    bool
	}{
		"none":       {0, false},
		"some":       {cleanupBatch - 1, false},
		"full batch": {cleanupBatch, true},
	}
	for kind, k := range kinds {
		for name, b := range batches {
			t.Run(kind+"/"+name, func(t *testing.T) {
				storage := &cleanupMemory{deleted: b.deleted}

				deleted, more, err := k.run(storage)
				require.NoError(t, err)

				assert.Equal(t, b.deleted, deleted)
				assert.Equal(t, b.more, more)
				assert.Equal(t, k.links, storage.links)
				assert.Equal(t, k.cutoff, storage.cutoff)
				assert.Equal(t, cleanupBatch, storage.limit)
			})
		}
	}
}

// A storage error reaches the caller unchanged, from either cleanup.
func TestCleanupError(t *testing.T) {
	lost := errors.New("connection lost")
	storage := &cleanupMemory{err: lost}

	_, _, linkErr := NewCleanup(storage, time.Now).DeleteExpired(t.Context())
	_, _, attemptErr := NewAttemptCleanup(storage, time.Now).DeleteExpired(t.Context())

	require.ErrorIs(t, linkErr, lost)
	require.ErrorIs(t, attemptErr, lost)
}
