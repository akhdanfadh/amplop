package httpapi

import (
	"fmt"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/validation"
)

const requiredFieldPrefix = "inbok-internal-required:"

// Schema preparation fills this map before requests start; requests only read it.
// Entries contain safe text from schema limits, never submitted values.
var inputMessages = map[string]string{}

// configureValidation remembers safe text before Huma caches its messages.
// Required fields also carry a private marker for the missing field name.
func configureValidation() {
	inputMessages[validation.MsgExpectedInteger] = "Enter a whole number."
	original := huma.ErrorFormatter
	huma.ErrorFormatter = func(format string, args ...any) string {
		if len(args) != 1 {
			return original(format, args...)
		}

		if format == validation.MsgExpectedRequiredProperty {
			field, ok := args[0].(string)
			if !ok {
				return original(format, args...)
			}
			return requiredFieldPrefix + field
		}

		raw := original(format, args...)
		if message := inputCorrection(format, args[0]); message != "" {
			inputMessages[raw] = message
		}
		return raw
	}
}

// inputCorrection formats only known rules whose arguments are public limits.
// Unknown templates and argument types keep the generic fallback.
func inputCorrection(format string, value any) string {
	switch format {
	case validation.MsgExpectedMinimumNumber, validation.MsgExpectedMaximumNumber:
		limit, ok := value.(float64)
		if !ok {
			return ""
		}
		if format == validation.MsgExpectedMinimumNumber {
			return fmt.Sprintf("Enter %v or more.", limit)
		}
		return fmt.Sprintf("Enter %v or less.", limit)
	case validation.MsgExpectedMinLength, validation.MsgExpectedMaxLength:
		limit, ok := value.(int)
		if !ok {
			return ""
		}
		unit := "characters"
		if limit == 1 {
			unit = "character"
		}
		if format == validation.MsgExpectedMinLength {
			return fmt.Sprintf("Use at least %d %s.", limit, unit)
		}
		return fmt.Sprintf("Use at most %d %s.", limit, unit)
	default:
		return ""
	}
}

// publicInputError builds safe correction text and precise required-field paths.
// Unrecognized Huma messages stay generic because they can contain input values.
func publicInputError(detail *huma.ErrorDetail) inputError {
	out := inputError{Location: detail.Location, Message: "Enter a valid value."}
	if message, found := inputMessages[detail.Message]; found {
		out.Message = message
		return out
	}

	field, required := strings.CutPrefix(detail.Message, requiredFieldPrefix)
	if !required || field == "" {
		return out
	}

	out.Location += "." + field
	out.Message = "This field is required."
	return out
}
