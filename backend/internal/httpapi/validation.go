package httpapi

import (
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/validation"
)

const requiredFieldPrefix = "inbok-internal-required:"

// configureValidation marks required-field names before Huma caches messages.
// Its string-only formatter needs a private marker to carry the field name.
func configureValidation() {
	original := huma.ErrorFormatter
	huma.ErrorFormatter = func(format string, args ...any) string {
		if format != validation.MsgExpectedRequiredProperty || len(args) != 1 {
			return original(format, args...)
		}

		field, ok := args[0].(string)
		if !ok {
			return original(format, args...)
		}

		return requiredFieldPrefix + field
	}
}

// publicInputError identifies the missing field so forms can mark its input.
// Other Huma messages stay generic because they can contain rejected values.
func publicInputError(detail *huma.ErrorDetail) inputError {
	out := inputError{Location: detail.Location, Message: "Enter a valid value."}
	field, required := strings.CutPrefix(detail.Message, requiredFieldPrefix)
	if !required || field == "" {
		return out
	}

	out.Location += "." + field
	out.Message = "This field is required."
	return out
}
