package httpapi

import (
	"net/http"
	"reflect"
	"slices"

	"github.com/danielgtaylor/huma/v2"
)

// problemTypeSchemaName names the contract's list of problem types.
const problemTypeSchemaName = "ProblemType"

// sharedProblems returns the problems in the API overview's Shared errors
// table that no operation shows as an example: request failures and the
// cross-origin refusal.
func sharedProblems() []*problem {
	problems := []*problem{crossOriginProblem()}
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

// registerProblemTypes adds the ProblemType schema to the contract, a string
// enum of every problem type an operation documents in an example and every
// shared one, sorted, and makes Problem.type any of those or any other URI
// reference. Call it once, after every operation is registered.
//
// NOTE: Huma drops schemas nothing references, and a closed enum on
// Problem.type would tell clients to reject a type missing from the list,
// e.g. one a newer server adds. anyOf keeps the list in the contract while
// the field stays open.
func registerProblemTypes(api huma.API) {
	doc := api.OpenAPI()
	types := documentedProblemTypes(doc)
	for _, p := range sharedProblems() {
		types = append(types, p.Type)
	}
	slices.Sort(types)
	types = slices.Compact(types)

	enum := make([]any, len(types))
	for i, typ := range types {
		enum[i] = typ
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
}

// documentedProblemTypes returns the type of every Problem Details example in
// doc's operation responses, in no particular order and possibly repeated.
func documentedProblemTypes(doc *huma.OpenAPI) []string {
	var types []string
	add := func(example any) {
		if p, ok := example.(*problem); ok {
			types = append(types, p.Type)
		}
	}
	for _, item := range doc.Paths {
		for _, op := range []*huma.Operation{item.Get, item.Put, item.Post, item.Delete, item.Options, item.Head, item.Patch, item.Trace} {
			if op == nil {
				continue
			}
			for _, response := range op.Responses {
				media := response.Content[problemContentType]
				if media == nil {
					continue
				}
				add(media.Example)
				for _, example := range media.Examples {
					add(example.Value)
				}
			}
		}
	}
	return types
}
