package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gin-gonic/gin"
)

const (
	problemContentType = "application/problem+json"
	problemPrefix      = apiPrefix + "/problems/"
)

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
