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
		response := postProbe(discard, &fakeProbes{total: 43}, c.body)
		if response.Code != http.StatusCreated {
			t.Fatalf("%s: %d %s", c.name, response.Code, response.Body.String())
		}

		body := response.Body.String()
		for _, want := range []string{`"id":7`, `"note":"hello"`, `"amount":3`, c.tag, `"createdAt":"2026-10-04T12:00:00Z"`, `"total":43`} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s: body lacks %s: %s", c.name, want, body)
			}
		}
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
		{"zero amount", `{"note":"x","amount":0,"highlighted":false}`, "body.amount", "Enter 1 or more."},
		{"large amount", `{"note":"x","amount":11,"highlighted":false}`, "body.amount", "Enter 10 or less."},
		{"fraction", `{"note":"x","amount":1.5,"highlighted":false}`, "body.amount", "Enter a whole number."},
		{"no highlighted", `{"note":"x","amount":1}`, "body.highlighted", "This field is required."},
		{"empty tag", `{"note":"x","amount":1,"highlighted":false,"tag":""}`, "body.tag", "Use at least 1 character."},
		{"long tag", `{"note":"x","amount":1,"highlighted":false,"tag":"` + strings.Repeat("t", 41) + `"}`, "body.tag", "Use at most 40 characters."},
	}

	for _, c := range cases {
		probes := &fakeProbes{}
		response := postProbe(discard, probes, c.body)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: %d %s", c.name, response.Code, response.Body.String())
		}
		problem := decodeJSON(t, response.Body.Bytes())
		if problem["type"] != "/api/v0/problems/validation-error" {
			t.Fatalf("%s: type = %v", c.name, problem["type"])
		}

		details, _ := decodeJSON(t, response.Body.Bytes())["errors"].([]any)
		if len(details) != 1 || details[0].(map[string]any)["location"] != c.location {
			t.Fatalf("%s: errors = %v, want one at %s", c.name, details, c.location)
		}
		if got := details[0].(map[string]any)["message"]; got != c.message {
			t.Fatalf("%s: message = %v, want %q", c.name, got, c.message)
		}
		if len(probes.added) != 0 {
			t.Fatalf("%s: added a rejected body", c.name)
		}
	}
}

// The full counter is a 409 with a fixed message.
func TestCreateProbeEventFull(t *testing.T) {
	response := postProbe(discard, &fakeProbes{err: probe.ErrCounterFull}, `{"note":"x","amount":6,"highlighted":false}`)

	body := decodeJSON(t, response.Body.Bytes())
	if response.Code != http.StatusConflict || body["type"] != "/api/v0/problems/counter-full" || body["detail"] != "The counter is full." || body["errors"] != nil {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
}

// A storage failure is a 500 that logs the cause and hides it.
func TestCreateProbeEventStorageError(t *testing.T) {
	var out bytes.Buffer
	probes := &fakeProbes{err: errors.New("lock probe counter: conn closed")}
	response := postProbe(slog.New(slog.NewTextHandler(&out, nil)), probes, `{"note":"x","amount":1,"highlighted":false}`)

	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "conn closed") {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(out.String(), `err="lock probe counter: conn closed"`) {
		t.Fatalf("log lacks the cause:\n%s", out.String())
	}
}

// Schema and operation names become generated client names, so pin them.
func TestProbeOpenAPI(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()

	path := doc.Paths["/probe/events"]
	if path == nil || path.Post == nil || path.Post.OperationID != "createProbeEvent" {
		t.Fatal("probe must document POST createProbeEvent")
	}

	request := path.Post.RequestBody.Content["application/json"].Schema.Ref
	if request != "#/components/schemas/NewProbeEvent" {
		t.Fatalf("request schema = %q", request)
	}

	responses := path.Post.Responses
	created := responses[strconv.Itoa(http.StatusCreated)]
	if created == nil || created.Content["application/json"].Schema.Ref != "#/components/schemas/ResponseProbeEventCreated" {
		t.Fatal("probe must document its 201 body")
	}
	if responses[strconv.Itoa(http.StatusConflict)] == nil {
		t.Fatal("probe must document the full counter's 409")
	}
	conflict := responses[strconv.Itoa(http.StatusConflict)].Content["application/problem+json"]
	example, err := json.Marshal(conflict.Example)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(example), `"type":"/api/v0/problems/counter-full"`) {
		t.Fatalf("409 example = %s", example)
	}

	event := doc.Components.Schemas.Map()["ProbeEvent"]
	if event == nil || event.Properties["createdAt"] == nil || event.Properties["tag"] == nil {
		t.Fatal("ProbeEvent must document createdAt and tag")
	}
}
