package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/validation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/session"
)

// Input details preserve nested paths and use each field's own correction rules.
func TestValidationDetails(t *testing.T) {
	// Given an operation that repeats the same field rules in a struct, an
	// array, a nested array, and a map.
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
		Metadata:      map[string]any{accessKey: session.AccessPublic},
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
			// When the body is sent.
			request := httptest.NewRequest(http.MethodPost, "/api/v0/required-inputs", strings.NewReader(c.body))
			request.Header.Set("Origin", "http://"+request.Host)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			a.Handler().ServeHTTP(response, request)

			// Then a valid body is created, and an invalid one gets a 422
			// listing each problem at its path, without echoing input.
			if c.want == nil {
				assert.Equal(t, http.StatusCreated, response.Code, response.Body.String())
				return
			}

			require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			for _, private := range []string{"PRIVATE-INPUT", "inbok-internal-", `"value"`} {
				assert.NotContains(t, response.Body.String(), private)
			}

			var body struct {
				Errors []struct{ Location, Message string }
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			var got []string
			for _, detail := range body.Errors {
				got = append(got, detail.Location+": "+detail.Message)
			}
			assert.ElementsMatch(t, c.want, got)
		})
	}

	// The private marker never reaches the contract either.
	doc, err := a.OpenAPI()
	require.NoError(t, err)
	assert.NotContains(t, string(doc), "inbok-internal-")
}

// A fraction beyond a bound gets one range correction without echoing its value.
func TestNumericCorrections(t *testing.T) {
	writes := &testWrites{}
	response := postTestWrite(discard, writes, `{"note":"ok","amount":11.5,"highlighted":false}`)

	require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	var body struct {
		Errors []struct{ Location, Message string }
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	var messages []string
	for _, detail := range body.Errors {
		assert.Equal(t, "body.amount", detail.Location)
		messages = append(messages, detail.Message)
	}
	assert.Equal(t, []string{"Enter a whole number from 1 to 10."}, messages)
	assert.NotContains(t, response.Body.String(), "11.5", "response echoes rejected input")
	assert.NotContains(t, response.Body.String(), `"value"`, "response echoes rejected input")
	assert.Empty(t, writes.added, "rejected input reached the service")
}

// A list over its maxItems gets the limit in words, as a length does.
func TestMaxItemsCorrection(t *testing.T) {
	assert.Equal(t, "Use at most 100 items.", inputCorrection(validation.MsgExpectedMaxItems, 100))
	assert.Equal(t, "Use at most 1 item.", inputCorrection(validation.MsgExpectedMaxItems, 1))
	assert.Empty(t, inputCorrection(validation.MsgExpectedMaxItems, "100"), "an unknown argument keeps the generic text")
}
