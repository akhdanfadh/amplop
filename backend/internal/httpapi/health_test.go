package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealth(t *testing.T) {
	router := New(discard, Services{}).Handler()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v0/health", nil))

	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "application/json", response.Header().Get("Content-Type"))

	// No $schema metadata: the body is exactly the data envelope.
	assert.JSONEq(t, `{"data": {"status": "ok"}}`, response.Body.String())

	assert.Empty(t, response.Header().Get("Link"))
}

func TestHealthOpenAPI(t *testing.T) {
	a := New(discard, Services{})
	router, api := a.Handler(), a.api
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v0/openapi.json", nil))

	require.Equal(t, http.StatusOK, response.Code)

	var document map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &document))
	assert.Regexp(t, `^3\.1\.`, document["openapi"])

	servers := api.OpenAPI().Servers
	require.Len(t, servers, 1)
	assert.Equal(t, "/api/v0", servers[0].URL)

	assert.Nil(t, api.OpenAPI().Paths["/api/v0/health"], "OpenAPI paths must not repeat the API base path")

	path := api.OpenAPI().Paths["/health"]
	require.NotNil(t, path)
	require.NotNil(t, path.Get, "health must document GET")
	assert.Nil(t, path.Head, "health must document GET only")
	assert.Equal(t, "getHealth", path.Get.OperationID)
	require.Len(t, path.Get.Responses, 2)
	fallback := path.Get.Responses["default"]
	require.NotNil(t, fallback)
	require.NotNil(t, fallback.Content[problemContentType])
	assert.Equal(t, "#/components/schemas/Problem", fallback.Content[problemContentType].Schema.Ref)

	status := strconv.Itoa(http.StatusOK)
	get := path.Get.Responses[status]
	require.NotNil(t, get)
	require.NotNil(t, get.Content["application/json"], "GET must document its JSON success response")

	// Schema names become generated client type names, so pin them.
	schemas := api.OpenAPI().Components.Schemas
	schema := get.Content["application/json"].Schema
	require.Equal(t, "#/components/schemas/ResponseHealthStatus", schema.Ref)
	schema = schemas.SchemaFromRef(schema.Ref)

	data := schema.Properties["data"]
	require.NotNil(t, data, "success schema must contain the data envelope")
	assert.Nil(t, schema.Properties["$schema"], "success schema must not document $schema metadata")

	require.Equal(t, "#/components/schemas/HealthStatus", data.Ref)
	data = schemas.SchemaFromRef(data.Ref)

	field := data.Properties["status"]
	require.NotNil(t, field)
	assert.Equal(t, "string", field.Type)
	assert.Equal(t, []any{"ok"}, field.Enum)
}
