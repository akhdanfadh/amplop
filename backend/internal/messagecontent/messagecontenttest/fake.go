// Package messagecontenttest holds what tests of code that uses a
// [messagecontent.Store] need: [Fake], a store in memory, and
// [RunContract], the tests every store passes, so the fake behaves as the
// real one does.
package messagecontenttest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"iter"
	"sync"
	"time"

	"amplop/internal/messagecontent"
)

// Fake is a [messagecontent.Store] in memory. The zero value is empty and
// ready. It is safe for concurrent use.
type Fake struct {
	// Fail, when set, is the error every method returns without doing
	// anything. Set it before goroutines share the fake.
	Fail error

	mu       sync.Mutex
	contents map[messagecontent.Ref]stored
}

// stored is one content the fake holds.
type stored struct {
	content []byte
	written time.Time
}

// Put stores content from its current offset under ref.
func (f *Fake) Put(ctx context.Context, ref messagecontent.Ref, content io.ReadSeeker) error {
	if err := f.refuse(ctx); err != nil {
		return err
	}
	b, err := io.ReadAll(content)
	if err != nil {
		return fmt.Errorf("read content: %w", err)
	}
	// A real store's upload fails when the caller gives up during it.
	if err := ctx.Err(); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.contents == nil {
		f.contents = make(map[messagecontent.Ref]stored)
	}
	f.contents[ref] = stored{content: b, written: time.Now()}
	return nil
}

// Get returns ref's content.
func (f *Fake) Get(ctx context.Context, ref messagecontent.Ref) (io.ReadCloser, error) {
	if err := f.refuse(ctx); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.contents[ref]
	if !ok {
		return nil, messagecontent.ErrNotStored
	}
	// Put never changes a stored slice, so readers may share it.
	return io.NopCloser(bytes.NewReader(s.content)), nil
}

// Delete removes the content of refs.
func (f *Fake) Delete(ctx context.Context, refs []messagecontent.Ref) error {
	if err := f.refuse(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ref := range refs {
		delete(f.contents, ref)
	}
	return nil
}

// DeleteAccount removes all content of account.
func (f *Fake) DeleteAccount(ctx context.Context, account int64) error {
	if err := f.refuse(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for ref := range f.contents {
		if ref.Account == account {
			delete(f.contents, ref)
		}
	}
	return nil
}

// ListOlder yields every content written before cutoff.
func (f *Fake) ListOlder(ctx context.Context, cutoff time.Time) iter.Seq2[messagecontent.Ref, error] {
	return func(yield func(messagecontent.Ref, error) bool) {
		if err := f.refuse(ctx); err != nil {
			yield(messagecontent.Ref{}, err)
			return
		}
		for _, ref := range f.older(cutoff) {
			if !yield(ref, nil) {
				return
			}
		}
	}
}

// older returns the refs of content written before cutoff.
func (f *Fake) older(cutoff time.Time) []messagecontent.Ref {
	f.mu.Lock()
	defer f.mu.Unlock()
	var refs []messagecontent.Ref
	for ref, s := range f.contents {
		if s.written.Before(cutoff) {
			refs = append(refs, ref)
		}
	}
	return refs
}

// refuse returns the error a method returns before doing anything:
// [Fake.Fail], or ctx's error.
func (f *Fake) refuse(ctx context.Context) error {
	if f.Fail != nil {
		return f.Fail
	}
	return ctx.Err()
}
