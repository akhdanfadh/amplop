package emailverification

import (
	"context"
	"time"
)

const (
	// cleanupBatch is the most expired links one [Cleanup.DeleteExpired]
	// deletes, so one statement stays short however many have piled up.
	cleanupBatch = 1000

	// cleanupGrace is how long after expiring a link is deleted. The
	// cleanup's clock is the worker's, and the API checks expiry with its
	// own; a worker clock running ahead by less than this cannot delete a
	// link the API still accepts.
	cleanupGrace = time.Hour
)

// cleanupStorage deletes expired links.
type cleanupStorage interface {
	// DeleteExpiredEmailVerificationLinks deletes up to limit links expiring at or before
	// expiredBy, skipping links another transaction holds, and returns how
	// many it deleted.
	DeleteExpiredEmailVerificationLinks(ctx context.Context, expiredBy time.Time, limit int) (int, error)
}

// Cleanup deletes expired verification links, which no one can use.
type Cleanup struct {
	storage cleanupStorage
	now     func() time.Time
}

// NewCleanup returns a [Cleanup] given storage and a clock.
func NewCleanup(storage cleanupStorage, now func() time.Time) *Cleanup {
	return &Cleanup{storage: storage, now: now}
}

// DeleteExpired deletes up to one batch of links that expired at least
// cleanupGrace ago. It returns how many it deleted, whether more may
// remain, and storage's error.
func (c *Cleanup) DeleteExpired(ctx context.Context) (int, bool, error) {
	deleted, err := c.storage.DeleteExpiredEmailVerificationLinks(ctx, c.now().Add(-cleanupGrace), cleanupBatch)
	if err != nil {
		return 0, false, err
	}
	// A short batch means none were left, apart from links another
	// transaction holds, which the next run deletes.
	return deleted, deleted == cleanupBatch, nil
}
