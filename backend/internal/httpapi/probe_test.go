package httpapi

import (
	"bytes"
	"context"
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

// fakeStorage keeps the probe counter in memory, saving nothing when the
// update fails.
type fakeStorage struct {
	total int64
	err   error // returned instead of saving, e.g. a lost connection
}

func (f *fakeStorage) UpdateCounter(_ context.Context, update func(c *probe.Counter) (probe.Entry, error)) (probe.Result, error) {
	counter := probe.Counter{Total: f.total}
	entry, err := update(&counter)
	if err != nil {
		return probe.Result{}, err
	}
	if f.err != nil {
		return probe.Result{}, f.err
	}

	f.total = counter.Total
	createdAt := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	return probe.Result{Event: probe.Event{Entry: entry, ID: 7, CreatedAt: createdAt}, Total: counter.Total}, nil
}

// postProbe sends body to the probe operation of an API saving to storage.
func postProbe(logger *slog.Logger, storage *fakeStorage, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v0/probe/events", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")

	response := httptest.NewRecorder()
	New(logger, probe.NewService(storage)).Handler().ServeHTTP(response, request)
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
		response := postProbe(discard, &fakeStorage{total: 40}, c.body)
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
		name, body, location string
	}{
		{"blank note", `{"note":" \t","amount":1,"highlighted":false}`, "body.note"},
		{"long note", `{"note":"` + strings.Repeat("n", 121) + `","amount":1,"highlighted":false}`, "body.note"},
		{"no note", `{"amount":1,"highlighted":false}`, "body"},
		{"zero amount", `{"note":"x","amount":0,"highlighted":false}`, "body.amount"},
		{"large amount", `{"note":"x","amount":11,"highlighted":false}`, "body.amount"},
		{"no highlighted", `{"note":"x","amount":1}`, "body"},
		{"empty tag", `{"note":"x","amount":1,"highlighted":false,"tag":""}`, "body.tag"},
		{"long tag", `{"note":"x","amount":1,"highlighted":false,"tag":"` + strings.Repeat("t", 41) + `"}`, "body.tag"},
	}

	for _, c := range cases {
		storage := &fakeStorage{}
		response := postProbe(discard, storage, c.body)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: %d %s", c.name, response.Code, response.Body.String())
		}

		details, _ := decodeJSON(t, response.Body.Bytes())["errors"].([]any)
		if len(details) != 1 || details[0].(map[string]any)["location"] != c.location {
			t.Fatalf("%s: errors = %v, want one at %s", c.name, details, c.location)
		}
		if storage.total != 0 {
			t.Fatalf("%s: saved a rejected body", c.name)
		}
	}
}

// The full counter is a 409 with a fixed message.
func TestCreateProbeEventFull(t *testing.T) {
	response := postProbe(discard, &fakeStorage{total: 995}, `{"note":"x","amount":6,"highlighted":false}`)

	body := decodeJSON(t, response.Body.Bytes())
	if response.Code != http.StatusConflict || body["detail"] != "counter is full" || body["errors"] != nil {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
}

// A storage failure is a 500 that logs the cause and hides it.
func TestCreateProbeEventStorageError(t *testing.T) {
	var out bytes.Buffer
	storage := &fakeStorage{err: errors.New("lock probe counter: conn closed")}
	response := postProbe(slog.New(slog.NewTextHandler(&out, nil)), storage, `{"note":"x","amount":1,"highlighted":false}`)

	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "conn closed") {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(out.String(), `err="lock probe counter: conn closed"`) {
		t.Fatalf("log lacks the cause:\n%s", out.String())
	}
}

// Schema and operation names become generated client names, so pin them.
func TestProbeOpenAPI(t *testing.T) {
	doc := New(discard, nil).api.OpenAPI()

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

	event := doc.Components.Schemas.Map()["ProbeEvent"]
	if event == nil || event.Properties["createdAt"] == nil || event.Properties["tag"] == nil {
		t.Fatal("ProbeEvent must document createdAt and tag")
	}
}
