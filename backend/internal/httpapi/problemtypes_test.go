package httpapi

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The contract lists every problem type an operation documents and every
// shared one, once each and sorted.
func TestProblemTypes(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()

	schema := doc.Components.Schemas.Map()[problemTypeSchemaName]

	require.NotNil(t, schema)
	assert.Equal(t, "string", schema.Type)
	types := make([]string, 0, len(schema.Enum))
	for _, value := range schema.Enum {
		types = append(types, value.(string))
	}
	assert.True(t, slices.IsSorted(types))
	assert.Equal(t, slices.Compact(slices.Clone(types)), types)
	for _, want := range []string{
		setupDoneType, sessionNotFoundType, emailAlreadyVerifiedType, emailVerificationLinkInvalidType,
		requestProblem(http.StatusNotFound).Type, requestProblem(http.StatusInternalServerError).Type,
		crossOriginProblem().Type, serviceBusyType,
	} {
		assert.Contains(t, types, want)
	}
}

// Every Problem Details example is a *problem, which the list reads; another
// value would leave its type out silently.
func TestProblemExamples(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()

	var examples []any
	for _, op := range operations(doc) {
		for _, response := range op.Responses {
			if media := response.Content[problemContentType]; media != nil {
				if media.Example != nil {
					examples = append(examples, media.Example)
				}
				for _, example := range media.Examples {
					examples = append(examples, example.Value)
				}
			}
		}
	}

	require.NotEmpty(t, examples)
	for _, example := range examples {
		assert.IsType(t, &problem{}, example)
	}
}

// Problem.type is a listed type or any other URI reference, and the exported
// document keeps the list.
func TestProblemTypeField(t *testing.T) {
	a := New(discard, Services{})

	doc, err := a.OpenAPI()
	require.NoError(t, err)

	var exported struct {
		Components struct {
			Schemas map[string]struct {
				Enum       []string                   `json:"enum"`
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	require.NoError(t, json.Unmarshal(doc, &exported))
	assert.Contains(t, exported.Components.Schemas[problemTypeSchemaName].Enum, setupDoneType)
	assert.JSONEq(t, `{"anyOf":[{"$ref":"#/components/schemas/ProblemType"},{"type":"string","format":"uri-reference"}]}`,
		string(exported.Components.Schemas["Problem"].Properties["type"]))
}

// The API overview's Problem types table has one row per listed type, and
// marks a shared type Any.
func TestProblemTypesTable(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()
	listed := doc.Components.Schemas.Map()[problemTypeSchemaName].Enum

	rows := 0
	for _, line := range strings.Split(doc.Info.Description, "\n") {
		if strings.HasPrefix(line, "| `/") {
			rows++
		}
	}
	assert.Equal(t, len(listed), rows)
	for _, typ := range listed {
		assert.Contains(t, doc.Info.Description, "| `"+typ.(string)+"` |")
	}
	assert.Contains(t, doc.Info.Description, "| `"+serviceBusyType+"` | 503 | Service busy | Any |")
	assert.Contains(t, doc.Info.Description, "| `"+setupDoneType+"` | 409 | Setup complete | Setup |")
}

// Each problem type has one status and one title wherever it is shown, so
// the table's row describes every use.
func TestProblemTypeTitles(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()
	first := map[string]*problem{}
	shown := sharedProblems()
	for _, example := range problemExamples(doc) {
		shown = append(shown, example.problem)
	}
	for _, p := range shown {
		if seen, ok := first[p.Type]; ok {
			assert.Equal(t, seen.Status, p.Status, p.Type)
			assert.Equal(t, seen.Title, p.Title, p.Type)
			continue
		}
		first[p.Type] = p
	}
}
