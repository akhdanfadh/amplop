package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const testAddr = "127.0.0.1:8080"

func TestAPIDocs(t *testing.T) {
	router := New(testAddr).Handler()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v0/docs", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}

	if !strings.Contains(response.Body.String(), "/api/v0/openapi.yaml") {
		t.Fatal("documentation must load the versioned OpenAPI document")
	}
}

func TestOpenAPI(t *testing.T) {
	a := New(testAddr)
	doc, err := a.OpenAPI()
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.HasSuffix(doc, []byte("}\n")) {
		t.Fatal("document must end with a newline")
	}

	var exported any
	if err := json.Unmarshal(doc, &exported); err != nil {
		t.Fatal(err)
	}

	// The exported document must match the one the server serves.
	response := httptest.NewRecorder()
	a.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v0/openapi.json", nil))

	var served any
	if err := json.Unmarshal(response.Body.Bytes(), &served); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(exported, served) {
		t.Fatalf("exported document differs from served document:\n%s", doc)
	}
}
