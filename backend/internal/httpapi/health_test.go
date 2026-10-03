package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestHealth(t *testing.T) {
	router := New(discard).Handler()
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

	// No $schema metadata: the body is exactly the data envelope.
	if len(body) != 1 {
		t.Fatalf("body must contain only data: %v", body)
	}

	if link := response.Header().Get("Link"); link != "" {
		t.Fatalf("Link = %q, want none", link)
	}
}

func TestHealthOpenAPI(t *testing.T) {
	a := New(discard)
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

	if servers := api.OpenAPI().Servers; len(servers) != 1 || servers[0].URL != "/api/v0" {
		t.Fatalf("API servers = %v, want the versioned base URL", servers)
	}

	if api.OpenAPI().Paths["/api/v0/health"] != nil {
		t.Fatal("OpenAPI paths must not repeat the API base path")
	}

	path := api.OpenAPI().Paths["/health"]
	if path == nil || path.Get == nil || path.Head != nil {
		t.Fatal("health must document GET only")
	}

	if path.Get.OperationID != "getHealth" {
		t.Fatal("health operation IDs changed")
	}

	status := strconv.Itoa(http.StatusOK)
	get := path.Get.Responses[status]
	if get == nil || get.Content["application/json"] == nil {
		t.Fatal("GET must document its JSON success response")
	}

	// Schema names become generated client type names, so pin them.
	schemas := api.OpenAPI().Components.Schemas
	schema := get.Content["application/json"].Schema
	if schema.Ref != "#/components/schemas/ResponseHealthStatus" {
		t.Fatalf("success schema = %q", schema.Ref)
	}
	schema = schemas.SchemaFromRef(schema.Ref)

	data := schema.Properties["data"]
	if data == nil {
		t.Fatal("success schema must contain the data envelope")
	}

	if schema.Properties["$schema"] != nil {
		t.Fatal("success schema must not document $schema metadata")
	}

	if data.Ref != "#/components/schemas/HealthStatus" {
		t.Fatalf("data schema = %q", data.Ref)
	}
	data = schemas.SchemaFromRef(data.Ref)

	field := data.Properties["status"]
	if field == nil || field.Type != "string" || !reflect.DeepEqual(field.Enum, []any{"ok"}) {
		t.Fatal("health status must be documented as the string enum ok")
	}
}
