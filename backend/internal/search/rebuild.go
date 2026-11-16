package search

import (
	"context"
	"fmt"
	"slices"
	"uuid"
)

// RebuildStorage reads what a rebuild compares members' indexes with.
type RebuildStorage interface {
	// UserIDs returns the ID of every member, with mail or not.
	UserIDs(ctx context.Context) ([]int64, error)

	// UserExists reports whether member owner exists.
	UserExists(ctx context.Context, owner int64) (bool, error)

	// MessageIDs returns the IDs of all of owner's messages, in any order.
	MessageIDs(ctx context.Context, owner int64) ([]uuid.UUID, error)
}

// indexing makes owner's messages with ids in the index what storage holds
// now, e.g. [Indexer.Index].
type indexing interface {
	Index(ctx context.Context, owner int64, ids []uuid.UUID) error
}

// Rebuild brings members' indexes back to what storage holds after the
// index lost or missed changes, e.g. a new, empty Meilisearch, documents
// an earlier release wrote, or a database restored from a backup.
type Rebuild struct {
	index   Index
	storage RebuildStorage
	indexer indexing
}

// NewRebuild returns a rebuild of index from what storage holds, writing
// each message as indexer does.
func NewRebuild(index Index, storage RebuildStorage, indexer indexing) *Rebuild {
	return &Rebuild{index: index, storage: storage, indexer: indexer}
}

// Rebuilt is what one member's rebuild did.
type Rebuilt struct {
	// Messages is how many of the member's messages it wrote.
	Messages int
	// Leftovers is how many documents it found of messages storage did not
	// return, and wrote again as storage holds them, which removes them
	// unless they came back since.
	Leftovers int
	// Removed is whether storage has no such member, so their index was
	// deleted whole.
	Removed bool
}

// Owners returns, sorted, every owner a rebuild covers: each member
// storage has, with mail or not, and each owner the index keeps documents
// for, e.g. a member a restored database no longer has.
func (r *Rebuild) Owners(ctx context.Context) ([]int64, error) {
	members, err := r.storage.UserIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	indexed, err := r.index.Owners(ctx)
	if err != nil {
		return nil, err
	}
	owners := slices.Concat(members, indexed)
	slices.Sort(owners)
	return slices.Compact(owners), nil
}

// Owner makes owner's index what storage holds now, in place: it writes
// each of owner's messages, then the index's documents of messages storage
// did not return, which removes them. It deletes the index whole when
// storage has no such member. It returns [ErrNoOwner] before reading
// anything.
//
// DESIGN: it never empties the index first, so the member's searches keep
// finding what the index held while it runs. Like [Indexer.Index], it
// needs no other write to owner's index while it runs, as the search
// queue runs one of owner's runs at a time (ADR005): it lists the index's
// documents page by page, and writes what it read earlier.
func (r *Rebuild) Owner(ctx context.Context, owner int64) (Rebuilt, error) {
	if err := ValidateOwner(owner); err != nil {
		return Rebuilt{}, err
	}

	// 1. Check the member exists now, not when the rebuild was asked for:
	// a restored database can give a removed member's ID to a new one.
	exists, err := r.storage.UserExists(ctx, owner)
	if err != nil {
		return Rebuilt{}, fmt.Errorf("check member: %w", err)
	}
	if !exists {
		return Rebuilt{Removed: true}, r.index.DeleteOwner(ctx, owner)
	}

	// 2. List the index's documents before writing any, so the listing's
	// pages do not shift under the rebuild's own writes.
	indexed, err := r.index.DocumentIDs(ctx, owner)
	if err != nil {
		return Rebuilt{}, err
	}

	// 3. Write every message storage has.
	ids, err := r.storage.MessageIDs(ctx, owner)
	if err != nil {
		return Rebuilt{}, fmt.Errorf("list messages: %w", err)
	}
	if err := r.write(ctx, owner, ids); err != nil {
		return Rebuilt{}, err
	}

	// 4. Write the documents step 3 did not reach. Indexing reads storage
	// again rather than deleting them blindly, so a message that came back
	// since is written, and every other one deleted.
	written := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		written[id] = true
	}
	leftovers := slices.DeleteFunc(indexed, func(id uuid.UUID) bool { return written[id] })
	if err := r.write(ctx, owner, leftovers); err != nil {
		return Rebuilt{}, err
	}
	return Rebuilt{Messages: len(ids), Leftovers: len(leftovers)}, nil
}

// write indexes owner's messages with ids, [BatchSize] at a time.
func (r *Rebuild) write(ctx context.Context, owner int64, ids []uuid.UUID) error {
	for batch := range slices.Chunk(ids, BatchSize) {
		if err := r.indexer.Index(ctx, owner, batch); err != nil {
			return err
		}
	}
	return nil
}
