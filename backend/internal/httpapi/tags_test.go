package httpapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every operation lists exactly one declared tag, and every declared tag
// groups at least one operation.
func TestTags(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()
	declared := make([]string, 0, len(doc.Tags))
	for _, tag := range doc.Tags {
		assert.NotEmpty(t, tag.Description, tag.Name)
		declared = append(declared, tag.Name)
	}

	used := map[string]bool{}
	for _, op := range operations(doc) {
		require.Len(t, op.Tags, 1, op.OperationID)
		assert.Contains(t, declared, op.Tags[0], op.OperationID)
		used[op.Tags[0]] = true
	}
	for _, name := range declared {
		assert.True(t, used[name], "tag %q groups no operation", name)
	}
}
