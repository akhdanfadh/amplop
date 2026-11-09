package connectedaccount

import (
	"context"
	"time"
)

const (
	// cleanupBatch is the most expired states one [StateCleanup.DeleteExpired]
	// deletes, so one statement stays short however many have piled up.
	cleanupBatch = 1000

	// cleanupGrace is how long after expiring a state is deleted. The
	// cleanup's clock is the worker's, and the API checks expiry with its
	// own; a worker clock running ahead by less than this cannot delete a
	// state the API still accepts.
	cleanupGrace = time.Hour
)

// cleanupStorage deletes expired connect states.
type cleanupStorage interface {
	// DeleteExpiredConnectStates deletes up to limit states expiring at or
	// before expiredBy, skipping states another transaction holds, and
	// returns how many it deleted.
	DeleteExpiredConnectStates(ctx context.Context, expiredBy time.Time, limit int) (int, error)
}

// StateCleanup deletes expired connect states: flows the member left at
// Google, whose states no callback took.
type StateCleanup struct {
	storage cleanupStorage
	now     func() time.Time
}

// NewStateCleanup returns a [StateCleanup] given storage and a clock.
func NewStateCleanup(storage cleanupStorage, now func() time.Time) *StateCleanup {
	return &StateCleanup{storage: storage, now: now}
}

// DeleteExpired deletes up to one batch of states that expired at least
// cleanupGrace ago. It returns how many it deleted, whether more may
// remain, and storage's error.
func (c *StateCleanup) DeleteExpired(ctx context.Context) (int, bool, error) {
	deleted, err := c.storage.DeleteExpiredConnectStates(ctx, c.now().Add(-cleanupGrace), cleanupBatch)
	if err != nil {
		return 0, false, err
	}
	// A short batch means none were left, apart from states another
	// transaction holds, which the next run deletes.
	return deleted, deleted == cleanupBatch, nil
}
