package httpapi

import (
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// problemTypeSchemaName names the contract's list of problem types.
const problemTypeSchemaName = "ProblemType"

// sharedProblems returns the problems any operation can answer, whether or
// not it shows them as examples: request failures, the cross-origin refusal,
// the refusal of a request of a kind the operation does not answer, and a
// request that timed out.
func sharedProblems() []*problem {
	problems := []*problem{crossOriginProblem(), fetchDestProblem(), serviceBusyProblem()}
	for _, status := range []int{
		http.StatusBadRequest,
		http.StatusNotFound,
		http.StatusMethodNotAllowed,
		http.StatusRequestTimeout,
		http.StatusRequestEntityTooLarge,
		http.StatusUnsupportedMediaType,
		http.StatusUnprocessableEntity,
		http.StatusInternalServerError,
	} {
		problems = append(problems, requestProblem(status))
	}
	return problems
}

// problemType is one problem type the contract documents.
type problemType struct {
	status int
	title  string
	// tags names the groups of the operations that show it as an example,
	// in the order doc lists them; it is nil for a shared type.
	tags []string
}

// registerProblemTypes documents every problem type an operation shows in
// an example and every shared one. It adds the ProblemType schema to the
// contract, a sorted string enum of those types, makes Problem.type any of
// them or any other URI reference, and appends a Problem types table to the
// API overview. Call it once, after every operation is registered.
//
// DESIGN: Huma drops schemas nothing references, and a closed enum on
// Problem.type would tell clients to reject a type missing from the list,
// e.g. one a newer server adds. anyOf keeps the list in the contract while
// the field stays open.
func registerProblemTypes(api huma.API) {
	doc := api.OpenAPI()
	types := problemTypes(doc)
	names := slices.Sorted(maps.Keys(types))

	enum := make([]any, len(names))
	for i, name := range names {
		enum[i] = name
	}
	schemas := doc.Components.Schemas
	schemas.Map()[problemTypeSchemaName] = &huma.Schema{
		Type:        "string",
		Description: "Problem types this API documents. Clients should still accept other types.",
		Enum:        enum,
	}

	problemSchema := schemas.SchemaFromRef(schemas.Schema(reflect.TypeFor[problem](), true, "").Ref)
	problemSchema.Properties["type"] = &huma.Schema{
		AnyOf: []*huma.Schema{
			{Ref: "#/components/schemas/" + problemTypeSchemaName},
			problemSchema.Properties["type"],
		},
	}

	doc.Info.Description += problemTypesTable(names, types)
}

// problemTypes returns every problem type doc's operations show as an
// example, and every shared one, by type.
func problemTypes(doc *huma.OpenAPI) map[string]problemType {
	order := make(map[string]int, len(doc.Tags))
	for i, tag := range doc.Tags {
		order[tag.Name] = i
	}

	types := map[string]problemType{}
	for _, shown := range problemExamples(doc) {
		p := shown.problem
		found := types[p.Type]
		found.status, found.title = p.Status, p.Title
		for _, tag := range shown.operation.Tags {
			if !slices.Contains(found.tags, tag) {
				found.tags = append(found.tags, tag)
			}
		}
		slices.SortFunc(found.tags, func(a, b string) int { return order[a] - order[b] })
		types[p.Type] = found
	}
	for _, p := range sharedProblems() {
		types[p.Type] = problemType{status: p.Status, title: p.Title}
	}
	return types
}

// problemExample is a Problem Details example and the operation that shows
// it.
type problemExample struct {
	problem   *problem
	operation *huma.Operation
}

// problemExamples returns every Problem Details example in doc's operation
// responses, in no particular order. An example that is not a *problem is
// left out.
func problemExamples(doc *huma.OpenAPI) []problemExample {
	var examples []problemExample
	add := func(op *huma.Operation, example any) {
		if p, ok := example.(*problem); ok {
			examples = append(examples, problemExample{p, op})
		}
	}
	for _, op := range operations(doc) {
		for _, response := range op.Responses {
			media := response.Content[problemContentType]
			if media == nil {
				continue
			}
			add(op, media.Example)
			for _, example := range media.Examples {
				add(op, example.Value)
			}
		}
	}
	return examples
}

// problemTypesTable returns the API overview's Problem types section: one
// row per name, in order, with its status, title, and where it is answered.
func problemTypesTable(names []string, types map[string]problemType) string {
	var b strings.Builder
	b.WriteString("\n## Problem types\n\n" +
		"Every problem type this API documents. Any marks a shared type, which\n" +
		"an operation can answer without documenting it; the others list the\n" +
		"groups whose operations document them.\n\n" +
		"| Type | Status | Title | Answered by |\n" +
		"| --- | --- | --- | --- |\n")
	for _, name := range names {
		typ := types[name]
		where := "Any"
		if typ.tags != nil {
			where = strings.Join(typ.tags, ", ")
		}
		fmt.Fprintf(&b, "| `%s` | %d | %s | %s |\n", name, typ.status, typ.title, where)
	}
	return b.String()
}
