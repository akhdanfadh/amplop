package passwordreset

import (
	"context"
	"time"
)

const (
	// cleanupBatch is the most rows one DeleteExpired deletes, so one
	// statement stays short however many have piled up.
	cleanupBatch = 1000

	// cleanupGrace is how long past its cutoff a row is deleted. The
	// cleanup's clock is the worker's, and the API checks links and limits
	// with its own; a worker clock running ahead by less than this cannot
	// delete a link the API still accepts or an attempt it still counts.
	cleanupGrace = time.Hour
)

// cleanupStorage deletes password reset rows no one can use.
type cleanupStorage interface {
	// DeleteExpiredPasswordResetLinks deletes up to limit links expiring at
	// or before expiredBy, skipping links another transaction holds, and
	// returns how many it deleted.
	DeleteExpiredPasswordResetLinks(ctx context.Context, expiredBy time.Time, limit int) (int, error)

	// DeleteOldPasswordResetAttempts deletes up to limit attempts whose
	// times are all at or before since, skipping attempts another
	// transaction holds, and returns how many it deleted.
	DeleteOldPasswordResetAttempts(ctx context.Context, since time.Time, limit int) (int, error)
}

// Cleanup deletes expired password reset links, which no one can use.
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
	deleted, err := c.storage.DeleteExpiredPasswordResetLinks(ctx, c.now().Add(-cleanupGrace), cleanupBatch)
	if err != nil {
		return 0, false, err
	}
	// A short batch means none were left, apart from links another
	// transaction holds, which the next run deletes.
	return deleted, deleted == cleanupBatch, nil
}

// AttemptCleanup deletes password reset attempts that no limit counts any
// more.
type AttemptCleanup struct {
	storage cleanupStorage
	now     func() time.Time
}

// NewAttemptCleanup returns an [AttemptCleanup] given storage and a clock.
func NewAttemptCleanup(storage cleanupStorage, now func() time.Time) *AttemptCleanup {
	return &AttemptCleanup{storage: storage, now: now}
}

// DeleteExpired deletes up to one batch of attempts older than the longest
// limit's window by at least cleanupGrace. It returns how many it deleted,
// whether more may remain, and storage's error.
func (c *AttemptCleanup) DeleteExpired(ctx context.Context) (int, bool, error) {
	deleted, err := c.storage.DeleteOldPasswordResetAttempts(ctx, c.now().Add(-attemptRetention-cleanupGrace), cleanupBatch)
	if err != nil {
		return 0, false, err
	}
	// A short batch means none were left, apart from attempts another
	// transaction holds, which the next run deletes.
	return deleted, deleted == cleanupBatch, nil
}
