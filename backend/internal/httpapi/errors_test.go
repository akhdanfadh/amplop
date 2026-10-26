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
	"strings"
	"sync"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, json.Unmarshal(data, &v), "json = %s", data)
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
		t.Run(c.note, func(t *testing.T) {
			var out bytes.Buffer
			response := post(errorAPI(slog.New(slog.NewJSONHandler(&out, nil))), `{"note":"`+c.note+`","password":"x"}`)

			require.Equal(t, http.StatusInternalServerError, response.Code)
			body := decodeJSON(t, response.Body.Bytes())
			assert.Equal(t, "/api/v0/problems/internal-error", body["type"])
			assert.Equal(t, "Internal error", body["title"])
			assert.InDelta(t, http.StatusInternalServerError, body["status"], 0)
			assert.Equal(t, c.detail, body["detail"])
			assert.Nil(t, body["errors"])
			assert.NotContains(t, response.Body.String(), "pq:", "body leaks the error")

			line, _, _ := strings.Cut(out.String(), "\n")
			entry := decodeJSON(t, []byte(line))
			assert.Equal(t, "unexpected error", entry["msg"])
			assert.Equal(t, "ERROR", entry["level"])
			assert.Equal(t, c.secret, entry["err"])
			assert.Equal(t, response.Header().Get(requestIDHeader), entry["request_id"])
		})
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
		t.Run(c.name, func(t *testing.T) {
			response := post(errorAPI(discard), c.body)

			require.Equal(t, c.status, response.Code)
			assert.NotContains(t, response.Body.String(), c.secret, "body echoes input")

			details, _ := decodeJSON(t, response.Body.Bytes())["errors"].([]any)
			require.NotEmpty(t, details)
			for _, d := range details {
				detail := d.(map[string]any)
				assert.NotContains(t, detail, "value")
				assert.NotNil(t, detail["location"])
				assert.NotNil(t, detail["message"])
			}
		})
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
		t.Run(c.note, func(t *testing.T) {
			response := post(errorAPI(discard), `{"note":"`+c.note+`","password":"x"}`)

			require.Equal(t, c.status, response.Code)
			body := decodeJSON(t, response.Body.Bytes())
			assert.Equal(t, c.detail, body["detail"])
			assert.NotContains(t, body, "errors")
		})
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
		assert.NotContains(t, response.Body.String(), "hunter2", "body echoes input")
	}

	assert.Equal(t, 2, strings.Count(out.String(), `"err":"disk full"`), "times the cause was logged:\n%s", out.String())
	details := sharedClient.(*huma.ErrorModel).Errors
	require.Len(t, details, 1, "shared client error changed")
	assert.Equal(t, "hunter2", details[0].Value, "shared client error changed")
}

// Other errors pass through unchanged.
func TestClientErrorUnchanged(t *testing.T) {
	response := post(errorAPI(discard), `{"note":"missing","password":"x"}`)

	require.Equal(t, http.StatusNotFound, response.Code)
	body := decodeJSON(t, response.Body.Bytes())
	assert.Equal(t, "The counter was not found.", body["detail"])
	assert.Equal(t, "Not found", body["title"])
}

func TestMappedServerError(t *testing.T) {
	var logs bytes.Buffer
	response := post(errorAPI(slog.New(slog.NewJSONHandler(&logs, nil))), `{"note":"unavailable","password":"x"}`)

	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	body := decodeJSON(t, response.Body.Bytes())
	assert.InDelta(t, http.StatusServiceUnavailable, body["status"], 0)
	assert.Equal(t, "/api/v0/problems/service-unavailable", body["type"])
	assert.Equal(t, "Maintenance is in progress. Try again later.", body["detail"])
	assert.Equal(t, "5", response.Header().Get("Retry-After"))
	assert.NotEmpty(t, response.Header().Get(requestIDHeader))
	assert.NotContains(t, logs.String(), "unexpected error", "mapped failure was logged as unexpected")
}

func TestBusinessInputError(t *testing.T) {
	response := post(errorAPI(discard), `{"note":"conflict","password":"x"}`)

	require.Equal(t, http.StatusConflict, response.Code)
	body := decodeJSON(t, response.Body.Bytes())
	assert.Equal(t, "/api/v0/problems/name-conflict", body["type"])
	assert.Equal(t, []any{map[string]any{"location": "body.note", "message": "This name is already used."}}, body["errors"])
}

func TestResponseFormatProblem(t *testing.T) {
	response := post(errorAPI(discard), `{"note":"format","password":"x"}`)

	require.Equal(t, http.StatusNotAcceptable, response.Code)
	body := decodeJSON(t, response.Body.Bytes())
	assert.InDelta(t, http.StatusNotAcceptable, body["status"], 0)
	assert.Equal(t, "/api/v0/problems/not-acceptable", body["type"])
	assert.Equal(t, "Response format not supported", body["title"])
	assert.Equal(t, "Ask for a JSON response.", body["detail"])
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

			require.Equal(t, c.status, response.Code)
			body := decodeJSON(t, response.Body.Bytes())
			assert.InDelta(t, c.status, body["status"], 0)
			assert.Equal(t, "/api/v0/problems/"+c.typ, body["type"])
		})
	}
}

func TestProblemSchema(t *testing.T) {
	a := New(discard, Services{})
	doc, err := a.OpenAPI()
	require.NoError(t, err)
	for _, private := range []string{`"value"`, `"raw"`, `"mapped"`, `"ErrorModel"`} {
		assert.NotContains(t, string(doc), private, "contract contains a private member")
	}

	schema := a.api.OpenAPI().Components.Schemas.Map()["Problem"]
	require.NotNil(t, schema, "public problem schema missing")
	assert.ElementsMatch(t, []string{"detail", "status", "title", "type"}, schema.Required)
	assert.Equal(t, "uri-reference", schema.Properties["type"].Format)

	errors := schema.Properties["errors"]
	assert.False(t, errors.Nullable)
	require.NotNil(t, errors.MinItems)
	assert.Equal(t, 1, *errors.MinItems)

	response := postProbe(discard, &fakeProbes{}, `{"amount":11.5}`)
	var body any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	result := &huma.ValidateResult{}
	huma.Validate(a.api.OpenAPI().Components.Schemas, schema, huma.NewPathBuffer(nil, 0), huma.ModeReadFromServer, body, result)
	assert.Empty(t, result.Errors, "HTTP response violates exported schema")
}

// Requests run concurrently after API construction, matching server startup.
// Checks inside the goroutines use assert: require must not run there.
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
					assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
					assert.Contains(t, response.Body.String(), "/api/v0/problems/validation-error")
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

	require.Equal(t, http.StatusUnprocessableEntity, response.Code)
	assert.Equal(t, problemContentType, response.Header().Get("Content-Type"))
	body := decodeJSON(t, response.Body.Bytes())
	assert.InDelta(t, http.StatusUnprocessableEntity, body["status"], 0)
	assert.NotNil(t, body["type"])
	assert.NotNil(t, body["title"])
	assert.NotNil(t, body["detail"])
}

func TestErrorConstructor(t *testing.T) {
	const childFlag = "INBOK_TEST_ERROR_CONSTRUCTOR"

	// Install the alternate constructor before the first API, in its own process.
	if os.Getenv(childFlag) == "" {
		binary, err := os.Executable()
		require.NoError(t, err)

		command := exec.Command(binary, "-test.run=^TestErrorConstructor$")
		command.Env = append(os.Environ(), childFlag+"=1")
		output, err := command.CombinedOutput()
		require.NoError(t, err, "alternate error constructor:\n%s", output)
		return
	}

	// Given, in the child: Huma builds errors with another constructor.
	huma.NewError = func(status int, message string, _ ...error) huma.StatusError {
		return newProblem(status, "/alternate-error", "Alternate error", message)
	}

	// When a handler returns an unexpected error.
	var logs bytes.Buffer
	response := post(errorAPI(slog.New(slog.NewJSONHandler(&logs, nil))), `{"note":"raw","password":"x"}`)

	// Then the response is still our fixed 500, and the cause is logged.
	require.Equal(t, http.StatusInternalServerError, response.Code)
	body := decodeJSON(t, response.Body.Bytes())
	assert.Equal(t, "/api/v0/problems/internal-error", body["type"])
	assert.Equal(t, "Something went wrong on our side.", body["detail"])
	assert.Contains(t, logs.String(), "pq: connection refused to 10.0.0.5:5432", "log lacks the cause")
}
