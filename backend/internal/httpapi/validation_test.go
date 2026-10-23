package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

// Input details preserve nested paths and use each field's own correction rules.
func TestValidationDetails(t *testing.T) {
	type fields struct {
		Name       string   `json:"name" minLength:"1" maxLength:"4"`
		Enabled    bool     `json:"enabled"`
		Count      *int     `json:"count,omitempty" minimum:"5" maximum:"10"`
		Price      *float64 `json:"price,omitempty" minimum:"-0.5" maximum:"1.5"`
		Restricted *int     `json:"restricted,omitempty" minimum:"1" maximum:"10" multipleOf:"2"`
		Code       *string  `json:"code,omitempty" minLength:"1" maxLength:"4" pattern:"^ok$"`
	}

	a := New(discard, Services{})
	huma.Register(a.api, huma.Operation{
		OperationID:   "validateRequiredInputs",
		Method:        http.MethodPost,
		Path:          "/required-inputs",
		DefaultStatus: http.StatusCreated,
	}, func(_ context.Context, _ *struct {
		Body struct {
			Name    string            `json:"name" maxLength:"4"`
			Enabled bool              `json:"enabled"`
			Profile *fields           `json:"profile,omitempty"`
			Items   []fields          `json:"items,omitempty"`
			Matrix  [][]fields        `json:"matrix,omitempty"`
			Values  map[string]fields `json:"values,omitempty"`
		}
	}) (*struct{}, error) {
		return nil, nil
	})

	cases := []struct {
		name, body string
		want       []string
	}{
		{"false is present", `{"name":"ok","enabled":false}`, nil},
		{"case insensitive", `{"name":"ok","ENABLED":false}`, nil},
		{"missing root fields", `{}`, []string{
			"body.enabled: This field is required.", "body.name: This field is required.",
		}},
		{"missing nested fields", `{"name":"ok","enabled":false,"profile":{}}`, []string{
			"body.profile.enabled: This field is required.", "body.profile.name: This field is required.",
		}},
		{"missing array field", `{"name":"ok","enabled":false,"items":[{"name":"ok","enabled":false},{"name":"ok","enabled":false},{"name":"ok","enabled":false},{"enabled":false}]}`, []string{
			"body.items[3].name: This field is required.",
		}},
		{"nested length range", `{"name":"ok","enabled":false,"profile":{"name":"","enabled":false}}`, []string{
			"body.profile.name: Use 1 to 4 characters.",
		}},
		{"same maximum different range", `{"name":"ok","enabled":false,"profile":{"name":"ok","enabled":false,"count":11}}`, []string{
			"body.profile.count: Enter a whole number from 5 to 10.",
		}},
		{"decimal range", `{"name":"ok","enabled":false,"profile":{"name":"ok","enabled":false,"price":1.6}}`, []string{
			"body.profile.price: Enter a number from -0.5 to 1.5.",
		}},
		{"array range", `{"name":"ok","enabled":false,"items":[{"name":"ok","enabled":false},{"name":"ok","enabled":false},{"name":"ok","enabled":false},{"name":"ok","enabled":false,"count":11}]}`, []string{
			"body.items[3].count: Enter a whole number from 5 to 10.",
		}},
		{"nested arrays", `{"name":"ok","enabled":false,"matrix":[[{"name":"ok","enabled":false,"count":11}]]}`, []string{
			"body.matrix[0][0].count: Enter a whole number from 5 to 10.",
		}},
		{"map path fallback", `{"name":"ok","enabled":false,"values":{"row":{"name":"ok","enabled":false,"count":11}}}`, []string{
			"body.values.row.count: Enter 10 or less.",
		}},
		{"additional numeric rule", `{"name":"ok","enabled":false,"profile":{"name":"ok","enabled":false,"restricted":12}}`, []string{
			"body.profile.restricted: Enter 10 or less.",
		}},
		{"pattern keeps its fallback", `{"name":"ok","enabled":false,"profile":{"name":"ok","enabled":false,"code":"no"}}`, []string{
			"body.profile.code: Enter a valid value.",
		}},
		{"wrong type", `{"name":"ok","enabled":"PRIVATE-INPUT"}`, []string{
			"body.enabled: Enter a valid value.",
		}},
		{"marker in rejected value", `{"name":"inbok-internal-required:PRIVATE-INPUT","enabled":false}`, []string{
			"body.name: Use at most 4 characters.",
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v0/required-inputs", strings.NewReader(c.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			a.Handler().ServeHTTP(response, request)
			if c.want == nil {
				if response.Code != http.StatusCreated {
					t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
				}
				return
			}

			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			for _, private := range []string{"PRIVATE-INPUT", "inbok-internal-", `"value"`} {
				if strings.Contains(response.Body.String(), private) {
					t.Fatalf("response contains %q: %s", private, response.Body.String())
				}
			}

			var body struct {
				Errors []struct{ Location, Message string }
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, detail := range body.Errors {
				got = append(got, detail.Location+": "+detail.Message)
			}
			sort.Strings(got)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("errors = %v, want %v", got, c.want)
			}
		})
	}

	doc, err := a.OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc), "inbok-internal-") {
		t.Fatal("OpenAPI contains a private validation marker")
	}
}

// A fraction beyond a bound gets one range correction without echoing its value.
func TestNumericCorrections(t *testing.T) {
	probes := &fakeProbes{}
	response := postProbe(discard, probes, `{"note":"ok","amount":11.5,"highlighted":false}`)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Errors []struct{ Location, Message string }
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var messages []string
	for _, detail := range body.Errors {
		if detail.Location != "body.amount" {
			t.Fatalf("location = %q", detail.Location)
		}
		messages = append(messages, detail.Message)
	}
	sort.Strings(messages)
	want := []string{"Enter a whole number from 1 to 10."}
	if !reflect.DeepEqual(messages, want) {
		t.Fatalf("messages = %v, want %v", messages, want)
	}
	if strings.Contains(response.Body.String(), "11.5") || strings.Contains(response.Body.String(), `"value"`) {
		t.Fatalf("response echoes rejected input: %s", response.Body.String())
	}
	if len(probes.added) != 0 {
		t.Fatal("rejected input reached the service")
	}
}
