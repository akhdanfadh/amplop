package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testAddr = "127.0.0.1:8080"

func TestAPIDocs(t *testing.T) {
	router, _ := New(testAddr)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v0/docs", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}

	if !strings.Contains(response.Body.String(), "/api/v0/openapi.yaml") {
		t.Fatal("documentation must load the versioned OpenAPI document")
	}
}
