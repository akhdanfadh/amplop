package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProblemSchema(t *testing.T) {
	a := New(discard, Services{})
	// Inspect schema members; OpenAPI response examples also use "value".
	doc, err := json.Marshal(a.api.OpenAPI().Components.Schemas)
	require.NoError(t, err)
	for _, private := range []string{`"value"`, `"raw"`, `"mapped"`, `"ErrorModel"`} {
		assert.NotContains(t, string(doc), private, "contract contains a private member")
	}

	schema := a.api.OpenAPI().Components.Schemas.Map()["Problem"]
	require.NotNil(t, schema, "public problem schema missing")
	assert.ElementsMatch(t, []string{"detail", "status", "title", "type"}, schema.Required)

	errors := schema.Properties["errors"]
	assert.False(t, errors.Nullable)
	require.NotNil(t, errors.MinItems)
	assert.Equal(t, 1, *errors.MinItems)

	response := postTestWrite(discard, &testWrites{}, `{"amount":11.5}`)
	var body any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	result := &huma.ValidateResult{}
	huma.Validate(a.api.OpenAPI().Components.Schemas, schema, huma.NewPathBuffer(nil, 0), huma.ModeReadFromServer, body, result)
	assert.Empty(t, result.Errors, "HTTP response violates exported schema")
}

// A link given with another email marks the email field, says what to type,
// and names no address.
func TestLinkEmailMismatchProblem(t *testing.T) {
	problem := linkEmailMismatchProblem()

	assert.Equal(t, http.StatusUnprocessableEntity, problem.Status)
	assert.Equal(t, []inputError{{Location: "body.email", Message: "Enter the email address this link was sent to."}}, problem.Errors)
}
