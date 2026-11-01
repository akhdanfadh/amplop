package httpapi

import (
	"log/slog"
	"net/http"
	"reflect"
	"strconv"
	"sync"

	"github.com/danielgtaylor/huma/v2"
)

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
	// A 429 is a refused attempt, which says when to retry.
	if throttled, ok := responses[strconv.Itoa(http.StatusTooManyRequests)]; ok {
		throttled.Headers = map[string]*huma.Param{retryAfterHeader: {
			Description: "Seconds until one more attempt fits every limit, rounded up.",
			Required:    true,
			Schema:      &huma.Schema{Type: huma.TypeInteger, Minimum: new(1.0)},
		}}
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
// An unauthenticated problem also clears the session cookie.
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
	// A session that ended after withAccess checked it, e.g. from another
	// browser, is cleared like one it refused. Huma sends the headers after
	// this hook runs.
	if p.Type == unauthenticatedType {
		cleared := sessionCookie("", -1)
		ctx.AppendHeader("Set-Cookie", cleared.String())
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
