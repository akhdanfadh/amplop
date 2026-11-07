package searchtest

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/search"
)

func TestFakeContract(t *testing.T) {
	RunContract(t, func(*testing.T) search.Index { return &Fake{} })
}

// [Fake.Document] shows what the fake holds, so a test can check what was
// indexed.
func TestFakeDocument(t *testing.T) {
	fake := &Fake{}
	added := document(asha, ashaWork, "invoice")
	upsert(t, fake, added)

	got, ok := fake.Document(asha, added.ID)
	_, missing := fake.Document(asha, document(asha, ashaWork, "other").ID)

	require.True(t, ok)
	assert.Equal(t, added, got)
	assert.False(t, missing)
}

// [Fake.Fail] makes every method return it and change nothing.
func TestFakeFail(t *testing.T) {
	lost := errors.New("connection lost")
	fake := &Fake{Fail: lost}
	added := document(asha, ashaWork, "invoice")

	_, searchErr := fake.Search(t.Context(), asha, query("invoice"))
	errs := []error{
		fake.Prepare(t.Context()),
		fake.Upsert(t.Context(), []search.Document{added}),
		fake.Delete(t.Context(), asha, nil),
		fake.DeleteAccount(t.Context(), asha, ashaWork),
		searchErr,
	}

	for _, err := range errs {
		require.ErrorIs(t, err, lost)
	}
	_, ok := fake.Document(asha, added.ID)
	assert.False(t, ok)
}
