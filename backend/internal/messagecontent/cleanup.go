package messagecontent

import (
	"context"
	"fmt"
	"time"
	"uuid"
)

const (
	// unusedAge is how old content no part names must be before
	// [Cleanup] deletes it, so a sweep never deletes content whose row is
	// still to commit; inbok's choice.
	unusedAge = 24 * time.Hour

	// cleanupBatch is the most content one [Cleanup.DeleteExpired] checks
	// with storage at once, and deletes.
	cleanupBatch = 1000
)

// Cleanup deletes stored content that no part names, e.g. what a save
// that failed after storing left behind: the scheduled cleanup's sweep.
type Cleanup struct {
	store   Store
	storage Storage
	now     func() time.Time
}

// NewCleanup returns the sweep of store's content against the parts in
// storage, taking the time from now.
func NewCleanup(store Store, storage Storage, now func() time.Time) *Cleanup {
	return &Cleanup{store: store, storage: storage, now: now}
}

// DeleteExpired deletes up to 1000 stored contents that no part names and
// that were last written over a day ago. It returns how many it deleted,
// and whether more may remain.
//
// DESIGN: The store lists keys in pages and cannot resume a listing, so
// each call lists from the start, stopping at the first batch it deletes
// from; orphans are rare, so a call usually lists the store once.
func (c *Cleanup) DeleteExpired(ctx context.Context) (int, bool, error) {
	cutoff := c.now().Add(-unusedAge)
	batch := make([]Ref, 0, cleanupBatch)
	for ref, err := range c.store.ListOlder(ctx, cutoff) {
		if err != nil {
			return 0, false, err
		}
		batch = append(batch, ref)
		if len(batch) < cleanupBatch {
			continue
		}
		deleted, err := c.deleteUnused(ctx, batch)
		if err != nil || deleted > 0 {
			return deleted, err == nil, err
		}
		batch = batch[:0]
	}
	deleted, err := c.deleteUnused(ctx, batch)
	return deleted, false, err
}

// deleteUnused deletes the content of those of refs that no part names,
// and returns how many.
func (c *Cleanup) deleteUnused(ctx context.Context, refs []Ref) (int, error) {
	if len(refs) == 0 {
		return 0, nil
	}
	ids := make([]uuid.UUID, len(refs))
	for i, ref := range refs {
		ids[i] = ref.ID
	}
	unused, err := c.storage.UnusedBlobs(ctx, ids)
	if err != nil {
		return 0, fmt.Errorf("find unused content: %w", err)
	}
	isUnused := make(map[uuid.UUID]bool, len(unused))
	for _, id := range unused {
		isUnused[id] = true
	}
	var orphans []Ref
	for _, ref := range refs {
		if isUnused[ref.ID] {
			orphans = append(orphans, ref)
		}
	}
	if len(orphans) == 0 {
		return 0, nil
	}
	if err := c.store.Delete(ctx, orphans); err != nil {
		return 0, fmt.Errorf("delete unused content: %w", err)
	}
	return len(orphans), nil
}
