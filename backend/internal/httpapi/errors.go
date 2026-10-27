package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"reflect"
	"strconv"
	"sync"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gin-gonic/gin"
)

// =========================
// Public response model
// =========================

const (
	problemContentType = "application/problem+json"
	problemPrefix      = apiPrefix + "/problems/"
)

const errorDocs = `## Shared errors

Errors use application/problem+json. The HTTP status describes the broad
failure; type identifies the specific problem. The body includes type,
status, title, and detail. Quote the X-Request-ID response header when
reporting a failure.

Each operation documents its specific failures with examples. Its default
response covers other errors using the same Problem schema, including:

| Status | Meaning |
| --- | --- |
| 400 | The request cannot be parsed. |
| 404 | The route or resource was not found. |
| 405 | The route does not support this method; see the Allow header. |
| 408 | Receiving the request body took too long. |
| 413 | The request body is too large. |
| 415 | The request content type is unsupported. |
| 422 | Parsed input violates the request rules. |
| 500 | An unexpected server failure occurred. |

Body errors apply to operations that accept a body. A default response
describes the body format, not a replacement HTTP status.

Input errors can include an errors list with field locations and safe
correction messages. For example, a 422 response from POST /setup:

    {
      "type": "/api/v0/problems/validation-error",
      "status": 422,
      "title": "Invalid request",
      "detail": "Correct the rejected request inputs.",
      "errors": [
        {"location": "body.password", "message": "Use 15 to 128 characters."}
      ]
    }

Use the actual HTTP status for request decisions. Handle unfamiliar problem
types by showing their safe detail. An intermediary, such as a proxy, may
return an error without a Problem Details body.
`

// inputError identifies rejected input without storing its value.
type inputError struct {
	Location string `json:"location"`
	Message  string `json:"message"`
}

// problem defines both the JSON response and Huma's generated error schema.
// Schema tags follow https://huma.rocks/features/request-validation/.
type problem struct {
	Type     string       `json:"type" format:"uri-reference"`
	Status   int          `json:"status" minimum:"100" maximum:"599"`
	Title    string       `json:"title"`
	Detail   string       `json:"detail"`
	Errors   []inputError `json:"errors,omitempty" nullable:"false" minItems:"1"`
	Instance string       `json:"instance,omitempty" format:"uri-reference"`

	// raw retains private diagnostics for sanitizing and logging, never JSON.
	raw *huma.ErrorModel
	// mapped marks the entire public response as explicitly selected as safe.
	mapped bool
}

// Error supplies the standard Go error message for this problem.
func (p *problem) Error() string { return p.Detail }

// GetStatus tells Huma which HTTP status to send.
func (p *problem) GetStatus() int { return p.Status }

// ContentType selects the Problem Details media type for Huma responses.
func (p *problem) ContentType(string) string { return problemContentType }

// =========================
// Problem constructors
// =========================

// newProblem constructs an explicitly mapped problem with safe public text.
// HTTP handlers choose its type, status, and any safe input messages.
func newProblem(status int, typ, title, detail string, inputs ...inputError) *problem {
	return &problem{Type: typ, Status: status, Title: title, Detail: detail, Errors: inputs, mapped: true}
}

// requestProblem supplies shared defaults for Huma and Gin failures by status.
// Business problems use newProblem with their own type and status.
func requestProblem(status int) *problem {
	switch status {
	case http.StatusBadRequest:
		return newProblem(status, problemPrefix+"invalid-request", "Invalid request", "The request could not be read. Check its format.")
	case http.StatusNotFound:
		return newProblem(status, problemPrefix+"not-found", "Not found", "The requested route or resource was not found.")
	case http.StatusMethodNotAllowed:
		return newProblem(status, problemPrefix+"method-not-allowed", "Method not allowed", "This route does not support that request method.")
	case http.StatusNotAcceptable:
		return newProblem(status, problemPrefix+"not-acceptable", "Response format not supported", "Ask for a JSON response.")
	case http.StatusRequestTimeout:
		return newProblem(status, problemPrefix+"request-timeout", "Request timeout", "Receiving the request took too long.")
	case http.StatusRequestEntityTooLarge:
		return newProblem(status, problemPrefix+"request-too-large", "Request too large", "The request body is too large.")
	case http.StatusUnsupportedMediaType:
		return newProblem(status, problemPrefix+"unsupported-media-type", "Unsupported media type", "Send the request as JSON.")
	case http.StatusUnprocessableEntity:
		return newProblem(status, problemPrefix+"validation-error", "Invalid request", "Correct the rejected request inputs.")
	default:
		return newProblem(http.StatusInternalServerError, problemPrefix+"internal-error", "Internal error", "Something went wrong on our side.")
	}
}

// =========================
// Huma integration
// =========================

// problemResponses keeps shared errors under default, without Huma's
// automatic 422 and 500 entries from Operation.Errors.
func problemResponses(api huma.API, statuses ...int) map[string]*huma.Response {
	schema := api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[problem](), true, "")
	responses := map[string]*huma.Response{
		"default": {Description: "Other errors. See Shared errors in the API overview."},
	}
	for _, status := range statuses {
		responses[strconv.Itoa(status)] = &huma.Response{Description: http.StatusText(status)}
	}
	for _, response := range responses {
		response.Content = map[string]*huma.MediaType{
			problemContentType: {Schema: schema},
		}
	}

	return responses
}

var errorSetup sync.Once

// configureErrors installs Huma's error constructor once before API creation.
// Runtime errors and exported schemas then share our public model.
func configureErrors() {
	errorSetup.Do(func() {
		configureValidation()
		huma.NewError = func(status int, message string, errs ...error) huma.StatusError {
			out := requestProblem(status)
			out.mapped = false
			out.raw = &huma.ErrorModel{
				Status: status,
				Detail: message,
				Errors: errorDetails(errs),
			}
			return out
		}
	})
}

// errorDetails preserves structured input details through ErrorDetailer.
// Plain errors supply diagnostic text; nil entries are skipped.
func errorDetails(errs []error) []*huma.ErrorDetail {
	var details []*huma.ErrorDetail
	for _, err := range errs {
		if err == nil {
			continue
		}

		if detailed, ok := err.(huma.ErrorDetailer); ok {
			details = append(details, detailed.ErrorDetail())
			continue
		}

		details = append(details, &huma.ErrorDetail{Message: err.Error()})
	}

	return details
}

// normalizeError applies our safety policy before Huma serializes a response.
// It keeps mapped problems, sanitizes others, and passes success bodies through.
func normalizeError(logger *slog.Logger, api huma.API, ctx huma.Context, value any) any {
	p, ok := value.(*problem)
	if !ok {
		// A helper may have created this error before New installed the hooks.
		raw, legacy := value.(*huma.ErrorModel)
		if !legacy {
			return value
		}
		p = requestProblem(raw.Status)
		p.mapped, p.raw = false, raw
	}
	if p.mapped {
		return p
	}

	out := *p
	out.Errors = nil
	if out.Status == http.StatusInternalServerError {
		logCause(ctx, logger, p.raw)
		return &out
	}

	// Build public input details without Huma's rejected values or raw text.
	seen := map[inputError]struct{}{}
	for _, detail := range p.raw.Errors {
		if detail == nil || detail.Location == "" {
			continue
		}
		input := publicInputError(api, ctx, detail)
		if _, duplicate := seen[input]; duplicate {
			continue
		}
		seen[input] = struct{}{}
		out.Errors = append(out.Errors, input)
	}

	return &out
}

// =========================
// Logging and Gin fallbacks
// =========================

// logCause writes technical causes to the server log with the request ID.
// Without detailed causes, it logs the original message instead.
func logCause(ctx huma.Context, logger *slog.Logger, raw *huma.ErrorModel) {
	logged := false
	for _, cause := range raw.Errors {
		if cause == nil {
			continue
		}
		logger.LogAttrs(ctx.Context(), slog.LevelError, "unexpected error", slog.String("err", cause.Message))
		logged = true
	}
	if !logged {
		logger.LogAttrs(ctx.Context(), slog.LevelError, "unexpected error", slog.String("err", raw.Detail))
	}
}

// writeProblem writes an already-safe problem for Gin fallbacks and recovery.
// These paths bypass Huma's response transformer.
func writeProblem(ctx *gin.Context, p *problem) {
	body, err := json.Marshal(p)
	if err != nil {
		ctx.AbortWithStatus(p.Status)
		return
	}

	// Replace the failed handler's content type, e.g. text/html after a panic,
	// so the client recognizes the replacement body as Problem Details.
	ctx.Header("Content-Type", problemContentType)
	ctx.Data(p.Status, problemContentType, body)
	ctx.Abort()
}
