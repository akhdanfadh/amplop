package messagecontenttest

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"

	"inbok/internal/messagecontent"
)

// Connected accounts in the contract. One's ID is a prefix of the other's,
// as a key prefix would be without its closing slash.
const (
	work int64 = 1
	home int64 = 11
)

// RunContract tests the behaviour every [messagecontent.Store] shares:
// reading back, replacing, read errors, deleting by ref and by account,
// listing by age, and cancellation. open returns a new, empty store for
// one test.
func RunContract(t *testing.T, open func(t *testing.T) messagecontent.Store) {
	t.Run("round trip", func(t *testing.T) {
		store := open(t)
		ref := newRef(work)

		put(t, store, ref, "<p>Hello</p>")

		assert.Equal(t, "<p>Hello</p>", read(t, store, ref))
	})

	t.Run("empty", func(t *testing.T) {
		store := open(t)
		ref := newRef(work)

		put(t, store, ref, "")

		assert.Empty(t, read(t, store, ref))
	})

	t.Run("replace", func(t *testing.T) {
		store := open(t)
		ref := newRef(work)

		put(t, store, ref, "first")
		put(t, store, ref, "second")

		assert.Equal(t, "second", read(t, store, ref))
	})

	t.Run("not stored", func(t *testing.T) {
		store := open(t)
		put(t, store, newRef(work), "other")

		_, err := store.Get(t.Context(), newRef(work))

		require.ErrorIs(t, err, messagecontent.ErrNotStored)
	})

	t.Run("offset", func(t *testing.T) {
		store := open(t)
		ref := newRef(work)
		content := strings.NewReader("skipped<p>Hello</p>")
		_, err := content.Seek(int64(len("skipped")), io.SeekStart)
		require.NoError(t, err)

		require.NoError(t, store.Put(t.Context(), ref, content))

		assert.Equal(t, "<p>Hello</p>", read(t, store, ref))
	})

	// The content in the read error tests never reads whole, so a store,
	// which keeps old content or all of the new, keeps none of it.
	t.Run("read error", func(t *testing.T) {
		store := open(t)
		ref := newRef(work)
		lost := errors.New("disk read failed")

		err := store.Put(t.Context(), ref, failingContent("abcdef", 3, lost))

		require.ErrorIs(t, err, lost)
		_, err = store.Get(t.Context(), ref)
		require.ErrorIs(t, err, messagecontent.ErrNotStored)
	})

	t.Run("read error keeps old content", func(t *testing.T) {
		store := open(t)
		ref := newRef(work)
		put(t, store, ref, "kept")
		lost := errors.New("disk read failed")

		err := store.Put(t.Context(), ref, failingContent("abcdef", 3, lost))

		require.ErrorIs(t, err, lost)
		assert.Equal(t, "kept", read(t, store, ref))
	})

	t.Run("delete", func(t *testing.T) {
		store := open(t)
		gone, kept, alsoGone := newRef(work), newRef(work), newRef(home)
		put(t, store, gone, "a")
		put(t, store, kept, "b")
		put(t, store, alsoGone, "c")

		err := store.Delete(t.Context(), []messagecontent.Ref{gone, newRef(work), alsoGone})

		require.NoError(t, err)
		requireNotStored(t, store, gone, alsoGone)
		assert.Equal(t, "b", read(t, store, kept))
	})

	t.Run("delete none", func(t *testing.T) {
		store := open(t)

		require.NoError(t, store.Delete(t.Context(), nil))
	})

	t.Run("delete account", func(t *testing.T) {
		store := open(t)
		first, second, other := newRef(work), newRef(work), newRef(home)
		put(t, store, first, "a")
		put(t, store, second, "b")
		put(t, store, other, "c")

		err := store.DeleteAccount(t.Context(), work)

		require.NoError(t, err)
		requireNotStored(t, store, first, second)
		assert.Equal(t, "c", read(t, store, other))
	})

	t.Run("delete account without content", func(t *testing.T) {
		store := open(t)

		require.NoError(t, store.DeleteAccount(t.Context(), work))
	})

	t.Run("list older", func(t *testing.T) {
		store := open(t)
		first, second := newRef(work), newRef(home)
		put(t, store, first, "a")
		put(t, store, second, "b")

		all := listOlder(t, store, time.Now().Add(time.Hour))
		none := listOlder(t, store, time.Now().Add(-time.Hour))

		assert.ElementsMatch(t, []messagecontent.Ref{first, second}, all)
		assert.Empty(t, none)
	})

	t.Run("list older by age", func(t *testing.T) {
		store := open(t)
		older := newRef(work)
		put(t, store, older, "a")
		// A store may keep times to the second, so the cutoff is a whole
		// second after the first write and the second write follows it.
		cutoff := nextSecond()
		newer := newRef(work)
		put(t, store, newer, "b")

		got := listOlder(t, store, cutoff)

		assert.Equal(t, []messagecontent.Ref{older}, got)
	})

	t.Run("list older stops early", func(t *testing.T) {
		store := open(t)
		put(t, store, newRef(work), "a")
		put(t, store, newRef(work), "b")

		seen := 0
		for _, err := range store.ListOlder(t.Context(), time.Now().Add(time.Hour)) {
			require.NoError(t, err)
			seen++
			break
		}

		assert.Equal(t, 1, seen)
	})

	t.Run("canceled", func(t *testing.T) {
		store := open(t)
		ref := newRef(work)
		put(t, store, ref, "kept")
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, getErr := store.Get(ctx, ref)
		var listErr error
		for _, err := range store.ListOlder(ctx, time.Now().Add(time.Hour)) {
			listErr = err
		}
		errs := []error{
			store.Put(ctx, newRef(work), strings.NewReader("a")),
			getErr,
			store.Delete(ctx, []messagecontent.Ref{ref}),
			store.DeleteAccount(ctx, work),
			listErr,
		}

		for _, err := range errs {
			require.ErrorIs(t, err, context.Canceled)
		}
		assert.Equal(t, "kept", read(t, store, ref))
	})

	t.Run("canceled while reading", func(t *testing.T) {
		store := open(t)
		ctx, cancel := context.WithCancel(t.Context())
		content := &cancelingReader{content: strings.NewReader("abc"), cancel: cancel}

		err := store.Put(ctx, newRef(work), content)

		// The store may have kept the content by then, so the test asks
		// only for the error.
		require.ErrorIs(t, err, context.Canceled)
	})
}

// cancelingReader reads its content and calls cancel when the content
// ends, as a caller giving up during an upload would.
type cancelingReader struct {
	content *strings.Reader
	cancel  context.CancelFunc
}

func (r *cancelingReader) Read(p []byte) (int, error) {
	n, err := r.content.Read(p)
	if err != nil {
		r.cancel()
	}
	return n, err
}

func (r *cancelingReader) Seek(offset int64, whence int) (int64, error) {
	return r.content.Seek(offset, whence)
}

// failingReader reads its content until offset failAt, where every read
// fails with err, however often the content is read again.
type failingReader struct {
	content *strings.Reader
	failAt  int64
	err     error
}

// failingContent returns a [failingReader] of s.
func failingContent(s string, failAt int64, err error) *failingReader {
	return &failingReader{content: strings.NewReader(s), failAt: failAt, err: err}
}

func (r *failingReader) Read(p []byte) (int, error) {
	offset := r.content.Size() - int64(r.content.Len())
	if offset >= r.failAt {
		return 0, r.err
	}
	if int64(len(p)) > r.failAt-offset {
		p = p[:r.failAt-offset]
	}
	return r.content.Read(p)
}

func (r *failingReader) Seek(offset int64, whence int) (int64, error) {
	return r.content.Seek(offset, whence)
}

// newRef returns a ref no other test uses, in account.
func newRef(account int64) messagecontent.Ref {
	return messagecontent.Ref{Account: account, ID: uuid.New()}
}

// put stores content under ref, failing the test on an error.
func put(t *testing.T, store messagecontent.Store, ref messagecontent.Ref, content string) {
	t.Helper()
	require.NoError(t, store.Put(t.Context(), ref, strings.NewReader(content)))
}

// read returns ref's content, failing the test on an error.
func read(t *testing.T, store messagecontent.Store, ref messagecontent.Ref) string {
	t.Helper()
	content, err := store.Get(t.Context(), ref)
	require.NoError(t, err)
	defer content.Close()
	b, err := io.ReadAll(content)
	require.NoError(t, err)
	return string(b)
}

// requireNotStored fails the test unless the store lacks every ref.
func requireNotStored(t *testing.T, store messagecontent.Store, refs ...messagecontent.Ref) {
	t.Helper()
	for _, ref := range refs {
		_, err := store.Get(t.Context(), ref)
		require.ErrorIs(t, err, messagecontent.ErrNotStored)
	}
}

// listOlder collects [messagecontent.Store.ListOlder], failing the test on
// an error.
func listOlder(t *testing.T, store messagecontent.Store, cutoff time.Time) []messagecontent.Ref {
	t.Helper()
	var refs []messagecontent.Ref
	for ref, err := range store.ListOlder(t.Context(), cutoff) {
		require.NoError(t, err)
		refs = append(refs, ref)
	}
	return refs
}

// nextSecond waits for the next whole second and returns it.
func nextSecond() time.Time {
	next := time.Now().Truncate(time.Second).Add(time.Second)
	time.Sleep(time.Until(next))
	return next
}
