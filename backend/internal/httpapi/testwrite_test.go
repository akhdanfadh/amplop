package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/session"
)

// testWritePath is where [newTestWriteAPI] registers the test-only write.
const testWritePath = "/test/writes"

// testWriteBody is the test-only write's body. Its tags hold one of each
// rule the contract uses: a not-blank text with a length limit, a bounded
// whole number, a required boolean, and an optional, nullable text.
type testWriteBody struct {
	Note        string  `json:"note" maxLength:"120" pattern:"\\S" patternDescription:"not blank"`
	Amount      int     `json:"amount" minimum:"1" maximum:"10"`
	Highlighted bool    `json:"highlighted"`
	Tag         *string `json:"tag,omitempty" nullable:"true" minLength:"1" maxLength:"40"`
}

// testWrites records each body the test-only write accepts and answers
// with err when it is set, e.g. a lost connection.
type testWrites struct {
	err   error
	added []testWriteBody
}

// newTestWriteAPI returns [New]'s API with a public JSON write at
// [testWritePath] that calls writes, so middleware and error tests run
// through every layer a real write passes without depending on a feature.
func newTestWriteAPI(logger *slog.Logger, writes *testWrites) *API {
	a := New(logger, Services{})
	huma.Register(a.api, huma.Operation{
		OperationID:   "createTestWrite",
		Metadata:      map[string]any{accessKey: session.AccessPublic},
		Method:        http.MethodPost,
		Path:          testWritePath,
		DefaultStatus: http.StatusCreated,
	}, func(_ context.Context, in *struct{ Body testWriteBody }) (*output[testWriteBody], error) {
		writes.added = append(writes.added, in.Body)
		if writes.err != nil {
			return nil, writes.err
		}
		return respondData(in.Body), nil
	})
	return a
}

// postTestWrite sends body as same-origin JSON to the test-only write of
// an API calling writes.
func postTestWrite(logger *slog.Logger, writes *testWrites, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, apiPrefix+testWritePath, strings.NewReader(body))
	request.Header.Set("Origin", "http://"+request.Host)
	request.Header.Set("Content-Type", "application/json")

	response := httptest.NewRecorder()
	newTestWriteAPI(logger, writes).Handler().ServeHTTP(response, request)
	return response
}

// Bodies that break the schema get a 422 naming the field and the rule.
func TestWriteValidation(t *testing.T) {
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
			writes := &testWrites{}
			response := postTestWrite(discard, writes, c.body)

			require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			problem := decodeJSON(t, response.Body.Bytes())
			assert.Equal(t, "/api/v0/problems/validation-error", problem["type"])

			details, _ := problem["errors"].([]any)
			require.Len(t, details, 1)
			detail, _ := details[0].(map[string]any)
			assert.Equal(t, c.location, detail["location"])
			assert.Equal(t, c.message, detail["message"])
			assert.Empty(t, writes.added, "added a rejected body")
		})
	}
}

// An unexpected service error is a 500 that logs the cause and hides it.
func TestWriteServiceError(t *testing.T) {
	var out bytes.Buffer
	writes := &testWrites{err: errors.New("save write: conn closed")}
	response := postTestWrite(slog.New(slog.NewTextHandler(&out, nil)), writes, `{"note":"x","amount":1,"highlighted":false}`)

	require.Equal(t, http.StatusInternalServerError, response.Code)
	assert.NotContains(t, response.Body.String(), "conn closed")
	assert.Contains(t, out.String(), `err="save write: conn closed"`, "log lacks the cause")
}
