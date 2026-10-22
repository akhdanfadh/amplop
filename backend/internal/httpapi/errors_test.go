package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"sync"
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
		&huma.ErrorDetail{Location: "body.note", Message: "rejected hunter2", Value: "hunter2"})
)

// errorAPI adds a test operation that fails on request.
func errorAPI(logger *slog.Logger) *API {
	a := New(logger, Services{})
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
			return nil, newProblem(http.StatusNotFound, problemPrefix+"not-found", "Not found", "The counter was not found.")
		case "nil client":
			return nil, newProblem(http.StatusConflict, problemPrefix+"counter-changed", "Counter changed", "The counter changed.")
		case "nil server":
			return nil, huma.Error500InternalServerError("storage failed", nil)
		case "shared server":
			return nil, sharedServer
		case "shared client":
			return nil, sharedClient
		case "unavailable":
			return nil, huma.ErrorWithHeaders(newProblem(http.StatusServiceUnavailable, problemPrefix+"service-unavailable", "Service unavailable", "Maintenance is in progress. Try again later."), http.Header{"Retry-After": {"5"}})
		case "conflict":
			return nil, newProblem(http.StatusConflict, problemPrefix+"name-conflict", "Name conflict", "Choose another name.", inputError{Location: "body.note", Message: "This name is already used."})
		case "format":
			return nil, huma.Error406NotAcceptable("private format diagnostic")
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

// Unclassified errors use fixed public text and log their cause with the request ID.
func TestHideServerErrors(t *testing.T) {
	cases := []struct {
		note, detail, secret string
	}{
		{"raw", "Something went wrong on our side.", "pq: connection refused to 10.0.0.5:5432"},
		{"explicit", "Something went wrong on our side.", "pq: password authentication failed"},
	}

	for _, c := range cases {
		var out bytes.Buffer
		response := post(errorAPI(slog.New(slog.NewJSONHandler(&out, nil))), `{"note":"`+c.note+`","password":"x"}`)

		body := decodeJSON(t, response.Body.Bytes())
		if response.Code != http.StatusInternalServerError || body["type"] != "/api/v0/problems/internal-error" || body["title"] != "Internal error" || body["status"] != float64(response.Code) || body["detail"] != c.detail || body["errors"] != nil {
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
		{"message", `{"note":"shared client","password":"x"}`, http.StatusUnprocessableEntity, "hunter2"},
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
		{"nil client", "The counter changed.", http.StatusConflict},
		{"nil server", "Something went wrong on our side.", http.StatusInternalServerError},
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
	if response.Code != http.StatusNotFound || body["detail"] != "The counter was not found." || body["title"] != "Not found" {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
}

func TestMappedServerError(t *testing.T) {
	var logs bytes.Buffer
	response := post(errorAPI(slog.New(slog.NewJSONHandler(&logs, nil))), `{"note":"unavailable","password":"x"}`)
	body := decodeJSON(t, response.Body.Bytes())
	if response.Code != http.StatusServiceUnavailable || body["status"] != float64(http.StatusServiceUnavailable) || body["type"] != "/api/v0/problems/service-unavailable" || body["detail"] != "Maintenance is in progress. Try again later." {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Retry-After") != "5" || response.Header().Get(requestIDHeader) == "" {
		t.Fatalf("headers = %v", response.Header())
	}
	if strings.Contains(logs.String(), "unexpected error") {
		t.Fatalf("mapped failure was logged as unexpected: %s", logs.String())
	}
}

func TestBusinessInputError(t *testing.T) {
	response := post(errorAPI(discard), `{"note":"conflict","password":"x"}`)
	body := decodeJSON(t, response.Body.Bytes())
	if response.Code != http.StatusConflict || body["type"] != "/api/v0/problems/name-conflict" {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
	details := body["errors"].([]any)
	detail := details[0].(map[string]any)
	if len(details) != 1 || len(detail) != 2 || detail["location"] != "body.note" || detail["message"] != "This name is already used." {
		t.Fatalf("details = %v", details)
	}
}

func TestResponseFormatProblem(t *testing.T) {
	response := post(errorAPI(discard), `{"note":"format","password":"x"}`)
	body := decodeJSON(t, response.Body.Bytes())
	if response.Code != http.StatusNotAcceptable || body["status"] != float64(http.StatusNotAcceptable) || body["type"] != "/api/v0/problems/not-acceptable" || body["title"] != "Response format not supported" || body["detail"] != "Ask for a JSON response." {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
}

type timedOutBody struct{}

func (timedOutBody) Read([]byte) (int, error) { return 0, os.ErrDeadlineExceeded }
func (timedOutBody) Close() error             { return nil }

func TestInputStatuses(t *testing.T) {
	const bodyLimitBytes = 1024 * 1024

	cases := []struct {
		name, body, contentType, typ string
		status                       int
	}{
		{"JSON", `{"note":`, "application/json", "invalid-request", http.StatusBadRequest},
		{"media", `{"note":"ok"}`, "text/plain", "unsupported-media-type", http.StatusUnsupportedMediaType},
		{"large", strings.Repeat("x", bodyLimitBytes+1), "application/json", "request-too-large", http.StatusRequestEntityTooLarge},
		{"timeout", "", "application/json", "request-timeout", http.StatusRequestTimeout},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v0/probe/events", strings.NewReader(c.body))
			request.Header.Set("Content-Type", c.contentType)
			if c.name == "timeout" {
				request.Body = timedOutBody{}
			}
			response := httptest.NewRecorder()
			New(discard, Services{}).Handler().ServeHTTP(response, request)
			body := decodeJSON(t, response.Body.Bytes())
			if response.Code != c.status || body["status"] != float64(c.status) || body["type"] != "/api/v0/problems/"+c.typ {
				t.Fatalf("%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestProblemSchema(t *testing.T) {
	a := New(discard, Services{})
	doc, err := a.OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{`"value"`, `"raw"`, `"mapped"`, `"ErrorModel"`} {
		if strings.Contains(string(doc), private) {
			t.Fatalf("contract contains private member %q", private)
		}
	}
	schema := a.api.OpenAPI().Components.Schemas.Map()["Problem"]
	if schema == nil {
		t.Fatal("public problem schema missing")
	}
	required := append([]string(nil), schema.Required...)
	sort.Strings(required)
	if !reflect.DeepEqual(required, []string{"detail", "status", "title", "type"}) || schema.Properties["type"].Format != "uri-reference" {
		t.Fatalf("schema = %+v", schema)
	}
	errors := schema.Properties["errors"]
	if errors.Nullable || errors.MinItems == nil || *errors.MinItems != 1 {
		t.Fatalf("errors = %+v", errors)
	}
	response := postProbe(discard, &fakeProbes{}, `{"amount":11.5}`)
	var body any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	result := &huma.ValidateResult{}
	huma.Validate(a.api.OpenAPI().Components.Schemas, schema, huma.NewPathBuffer(nil, 0), huma.ModeReadFromServer, body, result)
	if len(result.Errors) != 0 {
		t.Fatalf("HTTP response violates exported schema: %v", result.Errors)
	}
}

// Requests run concurrently after API construction, matching server startup.
func TestConcurrentErrors(t *testing.T) {
	const concurrentAPIs = 8
	const requestsPerAPI = 10

	a := New(discard, Services{})
	locals := make([]*API, concurrentAPIs)
	for i := range locals {
		locals[i] = New(discard, Services{})
	}

	var group sync.WaitGroup
	for _, local := range locals {
		group.Go(func() {
			for _, api := range []*API{a, local} {
				for range requestsPerAPI {
					response := httptest.NewRecorder()
					api.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v0/probe/events", strings.NewReader(`{}`)))
					if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "/api/v0/problems/validation-error") {
						t.Errorf("%d %s", response.Code, response.Body.String())
					}
				}
			}
		})
	}
	group.Wait()
}

func TestErrorNegotiation(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v0/probe/events", strings.NewReader(`{}`))
	request.Header.Set("Accept", "text/html")
	response := httptest.NewRecorder()
	New(discard, Services{}).Handler().ServeHTTP(response, request)
	body := decodeJSON(t, response.Body.Bytes())
	if response.Code != http.StatusUnprocessableEntity || response.Header().Get("Content-Type") != problemContentType || body["status"] != float64(response.Code) || body["type"] == nil || body["title"] == nil || body["detail"] == nil {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
}

func TestErrorConstructor(t *testing.T) {
	const childFlag = "INBOK_TEST_ERROR_CONSTRUCTOR"

	// Install the alternate constructor before the first API, in its own process.
	if os.Getenv(childFlag) == "" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}

		command := exec.Command(binary, "-test.run=^TestErrorConstructor$")
		command.Env = append(os.Environ(), childFlag+"=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("alternate error constructor: %v\n%s", err, output)
		}
		return
	}

	huma.NewError = func(status int, message string, _ ...error) huma.StatusError {
		return newProblem(status, "/alternate-error", "Alternate error", message)
	}

	var logs bytes.Buffer
	response := post(errorAPI(slog.New(slog.NewJSONHandler(&logs, nil))), `{"note":"raw","password":"x"}`)
	body := decodeJSON(t, response.Body.Bytes())
	if response.Code != http.StatusInternalServerError || body["type"] != "/api/v0/problems/internal-error" || body["detail"] != "Something went wrong on our side." {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(logs.String(), "pq: connection refused to 10.0.0.5:5432") {
		t.Fatalf("log lacks the cause: %s", logs.String())
	}
}
