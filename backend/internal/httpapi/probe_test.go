package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/probe"
)

// fakeProbes records each entry and answers with it saved as event 7, or
// with err, e.g. a domain error or a lost connection.
type fakeProbes struct {
	total int64
	err   error
	added []probe.Entry
}

func (f *fakeProbes) Add(_ context.Context, entry probe.Entry) (probe.Result, error) {
	f.added = append(f.added, entry)
	if f.err != nil {
		return probe.Result{}, f.err
	}

	createdAt := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	return probe.Result{Event: probe.Event{Entry: entry, ID: 7, CreatedAt: createdAt}, Total: f.total}, nil
}

// postProbe sends body to the probe operation of an API calling probes.
func postProbe(logger *slog.Logger, probes *fakeProbes, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v0/probe/events", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")

	response := httptest.NewRecorder()
	New(logger, Services{Probe: probes}).Handler().ServeHTTP(response, request)
	return response
}

func TestCreateProbeEvent(t *testing.T) {
	cases := []struct {
		name, body, tag string
	}{
		{"tag", `{"note":"hello","amount":3,"highlighted":true,"tag":"demo"}`, `"tag":"demo"`},
		{"null tag", `{"note":"hello","amount":3,"highlighted":false,"tag":null}`, `"tag":null`},
		{"no tag", `{"note":"hello","amount":3,"highlighted":false}`, `"tag":null`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			response := postProbe(discard, &fakeProbes{total: 43}, c.body)

			require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
			body := response.Body.String()
			for _, want := range []string{`"id":7`, `"note":"hello"`, `"amount":3`, c.tag, `"createdAt":"2026-10-04T12:00:00Z"`, `"total":43`} {
				assert.Contains(t, body, want)
			}
		})
	}
}

// Bodies that break the contract get a 422 naming the field.
func TestCreateProbeEventInvalid(t *testing.T) {
	cases := []struct {
		name, body, location, message string
	}{
		{"blank note", `{"note":" \t","amount":1,"highlighted":false}`, "body.note", "Enter a valid value."},
		{"long note", `{"note":"` + strings.Repeat("n", 121) + `","amount":1,"highlighted":false}`, "body.note", "Use at most 120 characters."},
		{"no note", `{"amount":1,"highlighted":false}`, "body.note", "This field is required."},
		{"zero amount", `{"note":"x","amount":0,"highlighted":false}`, "body.amount", "Enter a whole number from 1 to 10."},
		{"large amount", `{"note":"x","amount":11,"highlighted":false}`, "body.amount", "Enter a whole number from 1 to 10."},
		{"fraction", `{"note":"x","amount":1.5,"highlighted":false}`, "body.amount", "Enter a whole number from 1 to 10."},
		{"no highlighted", `{"note":"x","amount":1}`, "body.highlighted", "This field is required."},
		{"empty tag", `{"note":"x","amount":1,"highlighted":false,"tag":""}`, "body.tag", "Use 1 to 40 characters."},
		{"long tag", `{"note":"x","amount":1,"highlighted":false,"tag":"` + strings.Repeat("t", 41) + `"}`, "body.tag", "Use 1 to 40 characters."},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			probes := &fakeProbes{}
			response := postProbe(discard, probes, c.body)

			require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			problem := decodeJSON(t, response.Body.Bytes())
			assert.Equal(t, "/api/v0/problems/validation-error", problem["type"])

			details, _ := problem["errors"].([]any)
			require.Len(t, details, 1)
			detail, _ := details[0].(map[string]any)
			assert.Equal(t, c.location, detail["location"])
			assert.Equal(t, c.message, detail["message"])
			assert.Empty(t, probes.added, "added a rejected body")
		})
	}
}

// The full counter is a 409 with a fixed message.
func TestCreateProbeEventFull(t *testing.T) {
	response := postProbe(discard, &fakeProbes{err: probe.ErrCounterFull}, `{"note":"x","amount":6,"highlighted":false}`)

	require.Equal(t, http.StatusConflict, response.Code)
	body := decodeJSON(t, response.Body.Bytes())
	assert.Equal(t, "/api/v0/problems/counter-full", body["type"])
	assert.Equal(t, "The counter is full.", body["detail"])
	assert.Nil(t, body["errors"])
}

// A storage failure is a 500 that logs the cause and hides it.
func TestCreateProbeEventStorageError(t *testing.T) {
	var out bytes.Buffer
	probes := &fakeProbes{err: errors.New("lock probe counter: conn closed")}
	response := postProbe(slog.New(slog.NewTextHandler(&out, nil)), probes, `{"note":"x","amount":1,"highlighted":false}`)

	require.Equal(t, http.StatusInternalServerError, response.Code)
	assert.NotContains(t, response.Body.String(), "conn closed")
	assert.Contains(t, out.String(), `err="lock probe counter: conn closed"`, "log lacks the cause")
}

// Schema and operation names become generated client names, so pin them.
func TestProbeOpenAPI(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()

	path := doc.Paths["/probe/events"]
	require.NotNil(t, path)
	require.NotNil(t, path.Post, "probe must document POST")
	assert.Equal(t, "createProbeEvent", path.Post.OperationID)

	request := path.Post.RequestBody.Content["application/json"].Schema.Ref
	assert.Equal(t, "#/components/schemas/NewProbeEvent", request)

	responses := path.Post.Responses
	created := responses[strconv.Itoa(http.StatusCreated)]
	require.NotNil(t, created, "probe must document its 201 body")
	assert.Equal(t, "#/components/schemas/ResponseProbeEventCreated", created.Content["application/json"].Schema.Ref)

	full := responses[strconv.Itoa(http.StatusConflict)]
	require.NotNil(t, full, "probe must document the full counter's 409")
	conflict := full.Content["application/problem+json"]
	require.NotNil(t, conflict)
	example, err := json.Marshal(conflict.Example)
	require.NoError(t, err)
	assert.Contains(t, string(example), `"type":"/api/v0/problems/counter-full"`)

	event := doc.Components.Schemas.Map()["ProbeEvent"]
	require.NotNil(t, event)
	assert.NotNil(t, event.Properties["createdAt"])
	assert.NotNil(t, event.Properties["tag"])
}
