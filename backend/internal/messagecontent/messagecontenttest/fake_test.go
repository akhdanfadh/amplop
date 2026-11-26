package messagecontenttest

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"amplop/internal/messagecontent"
)

func TestFakeContract(t *testing.T) {
	RunContract(t, func(*testing.T) messagecontent.Store { return &Fake{} })
}

// [Fake.Fail] makes every method return it and change nothing.
func TestFakeFail(t *testing.T) {
	lost := errors.New("connection lost")
	fake := &Fake{}
	kept := newRef(work)
	put(t, fake, kept, "kept")
	fake.Fail = lost

	_, getErr := fake.Get(t.Context(), kept)
	var listErr error
	for _, err := range fake.ListOlder(t.Context(), time.Now().Add(time.Hour)) {
		listErr = err
	}
	errs := []error{
		fake.Put(t.Context(), newRef(work), strings.NewReader("a")),
		getErr,
		fake.Delete(t.Context(), []messagecontent.Ref{kept}),
		fake.DeleteAccount(t.Context(), work),
		listErr,
	}

	for _, err := range errs {
		require.ErrorIs(t, err, lost)
	}
	fake.Fail = nil
	require.Equal(t, "kept", read(t, fake, kept))
	require.Len(t, listOlder(t, fake, time.Now().Add(time.Hour)), 1)
}
