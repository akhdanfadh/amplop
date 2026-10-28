package httpapi

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorDocsExample(t *testing.T) {
	description := New(discard, Services{}).api.OpenAPI().Info.Description
	fence := regexp.MustCompile("(?s)(?:`{3,}|~{3,})json\\n(.*?)\\n(?:`{3,}|~{3,})")
	match := fence.FindStringSubmatch(description)

	// The docs renderer needs a JSON language label to preserve indentation.
	require.Len(t, match, 2, "API overview must contain a fenced JSON example")
	assert.True(t, json.Valid([]byte(match[1])), "example must be valid JSON")
	assert.Contains(t, match[1], "\n  \"type\":")
	assert.Contains(t, match[1], "\n    {\"location\":")
}
