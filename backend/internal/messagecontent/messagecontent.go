// Package messagecontent keeps message content: the bytes of text parts,
// attachments, and inline parts. Content lives in an object store, while
// PostgreSQL keeps what describes it and each content's ID (ADR009).
//
// messagecontent.go declares the store; part.go reads a message's part
// tree, text.go converts its text to UTF-8, and display.go chooses what a
// view shows of it.
package messagecontent

import (
	"context"
	"errors"
	"io"
	"iter"
	"time"

	"uuid"
)

// Ref names one stored content.
type Ref struct {
	Account int64     // the connected account the content came from
	ID      uuid.UUID // the content ID PostgreSQL keeps
}

// ErrNotStored means the store holds no content under a [Ref]. A row may
// still name it, e.g. after a lost volume: fetch the content again.
var ErrNotStored = errors.New("messagecontent: not stored")

// Store keeps content by [Ref]. It is safe for concurrent use.
//
// DESIGN: PostgreSQL and the store share no transaction, so a caller puts
// content before committing the row that names it, and deletes it after
// the row is gone. A failed write leaves at worst content no row names,
// which a sweep finds through [Store.ListOlder].
type Store interface {
	// Put stores content, from its current offset to its end, under ref,
	// replacing any content there, so a retry may repeat it. A store may
	// read content more than once, seeking back to that offset: to send a
	// hash of it ahead of it, so the content arrives whole, and to retry.
	// It returns content's read error, wrapped. After any error ref holds
	// its old content or all of the new, never part of either, e.g. when
	// an upload was stored but its answer lost: call Put again.
	Put(ctx context.Context, ref Ref, content io.ReadSeeker) error

	// Get returns ref's content as a stream, which the caller closes. It
	// returns [ErrNotStored] when the store holds none.
	Get(ctx context.Context, ref Ref) (io.ReadCloser, error)

	// Delete removes the content of refs; a ref the store lacks is not an error.
	Delete(ctx context.Context, refs []Ref) error

	// DeleteAccount removes all content of one connected account.
	DeleteAccount(ctx context.Context, account int64) error

	// ListOlder yields every stored content last written before cutoff, of
	// every account, in no set order. A store may keep times to the second
	// only. The listing stops at the first error, which it yields.
	ListOlder(ctx context.Context, cutoff time.Time) iter.Seq2[Ref, error]
}
