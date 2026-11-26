package s3

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"

	"amplop/internal/messagecontent"
)

// testID holds letters, so its upper-case spelling differs.
var testID = uuid.MustParse("0e6f3a52-9c1b-4d7e-8a2f-5b4c3d2e1f0a")

// Content sits at accounts/<account>/<blob ID>.
func TestContentKey(t *testing.T) {
	key := contentKey(messagecontent.Ref{Account: 42, ID: testID})

	assert.Equal(t, "accounts/42/0e6f3a52-9c1b-4d7e-8a2f-5b4c3d2e1f0a", key)
}

// Account 1's prefix does not match account 11's keys, so deleting
// account 1 leaves account 11's content.
func TestAccountPrefix(t *testing.T) {
	key := contentKey(messagecontent.Ref{Account: 11, ID: testID})

	assert.Equal(t, "accounts/1/", accountPrefix(1))
	assert.False(t, strings.HasPrefix(key, accountPrefix(1)))
}

func TestParseContentKey(t *testing.T) {
	for _, account := range []int64{1, 11, -5, math.MaxInt64} {
		ref := messagecontent.Ref{Account: account, ID: testID}

		got, ok := parseContentKey(contentKey(ref))

		require.True(t, ok, "account %d", account)
		assert.Equal(t, ref, got)
	}
}

// Only a key amplop writes parses. Any other spelling would give a ref
// whose key differs, so a sweep deleting that ref would leave the object.
func TestParseContentKeyForeign(t *testing.T) {
	id := testID.String()
	for _, key := range []string{
		"",
		"notes.txt",
		"Accounts/1/" + id,
		"accounts/",
		"accounts/1",
		"accounts/1/",
		"accounts//" + id,
		"accounts/x/" + id,
		"accounts/01/" + id,
		"accounts/+1/" + id,
		"accounts/-0/" + id,
		"accounts/9223372036854775808/" + id,
		"accounts/1/not-a-uuid",
		"accounts/1/" + strings.ToUpper(id),
		"accounts/1/{" + id + "}",
		"accounts/1/urn:uuid:" + id,
		"accounts/1/" + strings.ReplaceAll(id, "-", ""),
		"accounts/1/" + id + "/more",
		"accounts/1/" + id + "/",
	} {
		_, ok := parseContentKey(key)

		assert.False(t, ok, key)
	}
}
