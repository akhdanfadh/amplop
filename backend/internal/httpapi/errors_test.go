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

// errorAPI adds a test operation that fails on request.
func errorAPI(logger *slog.Logger) *API {
	a := New(logger)
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

// Other errors pass through unchanged, $schema included.
func TestClientErrorUnchanged(t *testing.T) {
	response := post(errorAPI(discard), `{"note":"missing","password":"x"}`)

	body := decodeJSON(t, response.Body.Bytes())
	if response.Code != http.StatusNotFound || body["detail"] != "counter not found" || body["$schema"] == nil {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
}
