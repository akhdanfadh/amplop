package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

type probeInput struct {
	Body struct {
		Note     string `json:"note" minLength:"1"`
		Password string `json:"password" maxLength:"4"`
	}
}

// Errors shared by every request, as a package-level var would be.
var (
	sharedServer = huma.Error500InternalServerError("storage failed", errors.New("disk full"))
	sharedClient = huma.Error422UnprocessableEntity("note rejected",
		&huma.ErrorDetail{Location: "body.note", Message: "already taken", Value: "hunter2"})
)

// errorAPI adds a test operation that fails on request.
func errorAPI(logger *slog.Logger) *API {
	a := New(logger, nil)
	huma.Register(a.api, huma.Operation{
		OperationID: "postProbe",
		Method:      http.MethodPost,
		Path:        "/probe",
	}, func(_ context.Context, in *probeInput) (*struct{}, error) {
		switch in.Body.Note {
		case "raw":
			return nil, errors.New("pq: connection refused to 10.0.0.5:5432")
		case "explicit":
			return nil, huma.Error500InternalServerError("storage failed", errors.New("pq: password authentication failed"))
		case "missing":
			return nil, huma.Error404NotFound("counter not found")
		case "nil client":
			return nil, huma.Error409Conflict("counter changed", nil)
		case "nil server":
			return nil, huma.Error500InternalServerError("storage failed", nil)
		case "shared server":
			return nil, sharedServer
		case "shared client":
			return nil, sharedClient
		}
		return nil, nil
	})
	return a
}

func post(a *API, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v0/probe", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")

	response := httptest.NewRecorder()
	a.Handler().ServeHTTP(response, request)
	return response
}

func decodeJSON(t *testing.T, data []byte) map[string]any {
	t.Helper()

	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("json = %s: %v", data, err)
	}
	return v
}

// A 5xx keeps its message but loses details, which are logged instead.
func TestHideServerErrors(t *testing.T) {
	cases := []struct {
		note, detail, secret string
	}{
		{"raw", "unexpected error occurred", "pq: connection refused to 10.0.0.5:5432"},
		{"explicit", "storage failed", "pq: password authentication failed"},
	}

	for _, c := range cases {
		var out bytes.Buffer
		response := post(errorAPI(slog.New(slog.NewJSONHandler(&out, nil))), `{"note":"`+c.note+`","password":"x"}`)

		body := decodeJSON(t, response.Body.Bytes())
		if response.Code != http.StatusInternalServerError || body["detail"] != c.detail || body["errors"] != nil {
			t.Fatalf("%s: %d %s", c.note, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "pq:") {
			t.Fatalf("%s: body leaks the error: %s", c.note, response.Body.String())
		}

		line, _, _ := strings.Cut(out.String(), "\n")
		entry := decodeJSON(t, []byte(line))
		if entry["msg"] != "unexpected error" || entry["level"] != "ERROR" || entry["err"] != c.secret ||
			entry["request_id"] != response.Header().Get(requestIDHeader) {
			t.Fatalf("%s: log entry = %v", c.note, entry)
		}
	}
}

// Client errors keep location and message but never echo input values.
func TestHideInputValues(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		secret     string
	}{
		{"validation", `{"note":"","password":"hunter2"}`, http.StatusUnprocessableEntity, "hunter2"},
		{"malformed", `{"password":"hunter2`, http.StatusBadRequest, "hunter2"},
	}

	for _, c := range cases {
		response := post(errorAPI(discard), c.body)
		if response.Code != c.status {
			t.Fatalf("%s: status = %d, want %d", c.name, response.Code, c.status)
		}
		if strings.Contains(response.Body.String(), c.secret) {
			t.Fatalf("%s: body echoes input: %s", c.name, response.Body.String())
		}

		details, _ := decodeJSON(t, response.Body.Bytes())["errors"].([]any)
		if len(details) == 0 {
			t.Fatalf("%s: details missing: %s", c.name, response.Body.String())
		}
		for _, d := range details {
			detail := d.(map[string]any)
			if _, ok := detail["value"]; ok || detail["location"] == nil || detail["message"] == nil {
				t.Fatalf("%s: detail = %v", c.name, detail)
			}
		}
	}
}

// A nil error passed to a Huma helper leaves a nil detail, which must
// neither turn the response into a 500 nor reach the client as null.
func TestNilErrorDetail(t *testing.T) {
	cases := []struct {
		note, detail string
		status       int
	}{
		{"nil client", "counter changed", http.StatusConflict},
		{"nil server", "storage failed", http.StatusInternalServerError},
	}

	for _, c := range cases {
		response := post(errorAPI(discard), `{"note":"`+c.note+`","password":"x"}`)

		body := decodeJSON(t, response.Body.Bytes())
		if response.Code != c.status || body["detail"] != c.detail {
			t.Fatalf("%s: %d %s", c.note, response.Code, response.Body.String())
		}
		if _, ok := body["errors"]; ok {
			t.Fatalf("%s: errors present: %s", c.note, response.Body.String())
		}
	}
}

// Hiding details must not change a shared error: the next request would
// lose its details, and concurrent requests would race.
func TestSharedErrorUnchanged(t *testing.T) {
	var out bytes.Buffer
	a := errorAPI(slog.New(slog.NewJSONHandler(&out, nil)))

	for range 2 {
		post(a, `{"note":"shared server","password":"x"}`)
		response := post(a, `{"note":"shared client","password":"x"}`)
		if strings.Contains(response.Body.String(), "hunter2") {
			t.Fatalf("body echoes input: %s", response.Body.String())
		}
	}

	if n := strings.Count(out.String(), `"err":"disk full"`); n != 2 {
		t.Fatalf("logged the cause %d times, want 2:\n%s", n, out.String())
	}
	details := sharedClient.(*huma.ErrorModel).Errors
	if len(details) != 1 || details[0].Value != "hunter2" {
		t.Fatalf("shared client error changed: %+v", details)
	}
}

// Other errors pass through unchanged.
func TestClientErrorUnchanged(t *testing.T) {
	response := post(errorAPI(discard), `{"note":"missing","password":"x"}`)

	body := decodeJSON(t, response.Body.Bytes())
	if response.Code != http.StatusNotFound || body["detail"] != "counter not found" || body["title"] != "Not Found" {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
}
