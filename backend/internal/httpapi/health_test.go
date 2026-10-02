package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestHealth(t *testing.T) {
	router := New(testAddr).Handler()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v0/health", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}

	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}

	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(body["data"], map[string]any{"status": "ok"}) {
		t.Fatalf("data = %v", body["data"])
	}

	schemaURL, ok := body["$schema"].(string)
	if !ok || len(body) != 2 {
		t.Fatalf("body must contain data and schema metadata: %v", body)
	}

	schema, err := url.Parse(schemaURL)
	if err != nil || !schema.IsAbs() || !strings.HasPrefix(schema.Path, "/api/v0/schemas/") {
		t.Fatalf("schema URL = %q", schemaURL)
	}

	if link := response.Header().Get("Link"); !strings.Contains(link, "<"+schema.Path+">") {
		t.Fatalf("Link = %q, schema URL = %q", link, schemaURL)
	}

	// The advertised schema must be served by the same API.
	schemaResponse := httptest.NewRecorder()
	router.ServeHTTP(schemaResponse, httptest.NewRequest(http.MethodGet, schema.Path, nil))
	if schemaResponse.Code != http.StatusOK || !json.Valid(schemaResponse.Body.Bytes()) {
		t.Fatalf("schema response = %d %s", schemaResponse.Code, schemaResponse.Body.String())
	}

	var definition struct {
		Properties map[string]struct {
			Ref string `json:"$ref"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schemaResponse.Body.Bytes(), &definition); err != nil {
		t.Fatal(err)
	}

	dataRef, err := url.Parse(definition.Properties["data"].Ref)
	if err != nil || dataRef.Path == "" {
		t.Fatalf("data schema reference = %q", definition.Properties["data"].Ref)
	}

	dataURL := schema.ResolveReference(dataRef)
	dataResponse := httptest.NewRecorder()
	router.ServeHTTP(dataResponse, httptest.NewRequest(http.MethodGet, dataURL.Path, nil))

	if dataResponse.Code != http.StatusTemporaryRedirect {
		t.Fatalf("unprefixed schema status = %d, want %d", dataResponse.Code, http.StatusTemporaryRedirect)
	}

	location := dataResponse.Header().Get("Location")
	if location != "/api/v0"+dataURL.Path {
		t.Fatalf("schema redirect = %q", location)
	}

	dataResponse = httptest.NewRecorder()
	router.ServeHTTP(dataResponse, httptest.NewRequest(http.MethodGet, location, nil))
	if dataResponse.Code != http.StatusOK || !json.Valid(dataResponse.Body.Bytes()) {
		t.Fatalf("data schema %q = %d %s", location, dataResponse.Code, dataResponse.Body.String())
	}
}

func TestHealthHead(t *testing.T) {
	router := New(testAddr).Handler()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/api/v0/health", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}

	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}

	if response.Body.Len() != 0 {
		t.Fatalf("HEAD body = %q", response.Body.String())
	}
}

func TestHealthOpenAPI(t *testing.T) {
	a := New(testAddr)
	router, api := a.Handler(), a.api
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v0/openapi.json", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}

	var document map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}

	if version, ok := document["openapi"].(string); !ok || !strings.HasPrefix(version, "3.1.") {
		t.Fatalf("OpenAPI version = %v", document["openapi"])
	}

	if servers := api.OpenAPI().Servers; len(servers) != 1 || servers[0].URL != "http://127.0.0.1:8080/api/v0" {
		t.Fatalf("API servers = %v, want the versioned base URL", servers)
	}

	if api.OpenAPI().Paths["/api/v0/health"] != nil {
		t.Fatal("OpenAPI paths must not repeat the API base path")
	}

	path := api.OpenAPI().Paths["/health"]
	if path == nil || path.Get == nil || path.Head == nil {
		t.Fatal("health GET and HEAD must be documented")
	}

	if path.Get.OperationID != "getHealth" || path.Head.OperationID != "headHealth" {
		t.Fatal("health operation IDs changed")
	}

	status := strconv.Itoa(http.StatusOK)
	get := path.Get.Responses[status]
	if get == nil || get.Content["application/json"] == nil {
		t.Fatal("GET must document its JSON success response")
	}

	schemas := api.OpenAPI().Components.Schemas
	schema := get.Content["application/json"].Schema
	if schema.Ref != "" {
		schema = schemas.SchemaFromRef(schema.Ref)
	}

	data := schema.Properties["data"]
	if data == nil {
		t.Fatal("success schema must contain the data envelope")
	}

	metadata := schema.Properties["$schema"]
	if metadata == nil || metadata.Type != "string" || !metadata.ReadOnly {
		t.Fatal("success schema must document its read-only schema metadata")
	}

	wantSchemaURL := "http://127.0.0.1:8080/api/v0/schemas/HealthResponse.json"
	if !reflect.DeepEqual(metadata.Examples, []any{wantSchemaURL}) {
		t.Fatalf("schema URL examples = %v, want %q", metadata.Examples, wantSchemaURL)
	}

	if data.Ref != "" {
		data = schemas.SchemaFromRef(data.Ref)
	}

	field := data.Properties["status"]
	if field == nil || field.Type != "string" || !reflect.DeepEqual(field.Enum, []any{"ok"}) {
		t.Fatal("health status must be documented as the string enum ok")
	}

	head := path.Head.Responses[status]
	if head == nil || len(head.Content) != 0 {
		t.Fatal("HEAD must document a bodyless success response")
	}
}
