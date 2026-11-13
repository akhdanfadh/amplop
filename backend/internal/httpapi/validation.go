package httpapi

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/validation"
)

// ========================
// Message preparation
// ========================

const requiredFieldPrefix = "inbok-internal-required:"

// Schema preparation fills this map before requests start; requests only read it.
// Entries contain safe text from schema limits, never submitted values.
var inputMessages = map[string]string{}

// emailFormat is the contract's format for an email address field (JSON
// Schema's "idn-email", RFC 6531 addresses).
const emailFormat = "idn-email"

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
	case validation.MsgExpectedMaxItems:
		limit, ok := value.(int)
		if !ok {
			return ""
		}
		return maxItemsMessage(limit)
	default:
		return ""
	}
}

// maxItemsMessage returns the correction for a list longer than limit.
func maxItemsMessage(limit int) string {
	if limit == 1 {
		return "Use at most 1 item."
	}
	return fmt.Sprintf("Use at most %d items.", limit)
}

// ========================
// Public input errors
// ========================

// publicInputError builds safe correction text and precise required-field paths.
// Unrecognized Huma messages stay generic because they can contain input values.
func publicInputError(api huma.API, ctx huma.Context, detail *huma.ErrorDetail) inputError {
	out := inputError{Location: detail.Location, Message: "Enter a valid value."}
	if message, found := inputMessages[detail.Message]; found {
		out.Message = message
		if combined := rangeCorrection(inputSchema(api, ctx.Operation(), detail.Location)); combined != "" {
			out.Message = combined
		}
		return out
	}

	// Every other failure of an address field, e.g. a malformed address or
	// a number in its place, reads as invalidEmailProblem's does.
	if schema := inputSchema(api, ctx.Operation(), detail.Location); schema != nil && schema.Format == emailFormat {
		out.Message = invalidEmailMessage
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

// rangeCorrection combines simple field limits without replacing other rules.
// More restrictive numeric rules keep their original correction messages.
func rangeCorrection(schema *huma.Schema) string {
	if schema == nil || len(schema.Enum) > 0 || schema.Const != nil {
		return ""
	}

	switch schema.Type {
	case huma.TypeInteger, huma.TypeNumber:
		if schema.Minimum == nil || schema.Maximum == nil {
			return ""
		}
		if schema.MultipleOf != nil || schema.ExclusiveMinimum != nil || schema.ExclusiveMaximum != nil {
			return ""
		}
		kind := "a number"
		if schema.Type == huma.TypeInteger {
			kind = "a whole number"
		}
		return fmt.Sprintf("Enter %s from %v to %v.", kind, *schema.Minimum, *schema.Maximum)
	case huma.TypeString:
		if schema.MinLength == nil || schema.MaxLength == nil {
			return ""
		}
		return fmt.Sprintf("Use %d to %d characters.", *schema.MinLength, *schema.MaxLength)
	default:
		return ""
	}
}

// ========================
// Schema lookup
// ========================

// inputSchema follows ordinary body field paths, including nested array indexes.
// Unsupported paths keep the existing correction instead of guessing a range.
func inputSchema(api huma.API, op *huma.Operation, location string) *huma.Schema {
	path, bodyField := strings.CutPrefix(location, "body.")
	if !bodyField || op.RequestBody == nil || op.RequestBody.Content["application/json"] == nil {
		return nil
	}

	schema := op.RequestBody.Content["application/json"].Schema
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '.' || r == '[' || r == ']' })
	for _, part := range parts {
		schema = plainSchema(api, schema)
		if schema == nil {
			return nil
		}
		if schema.Type == huma.TypeArray {
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 {
				return nil
			}
			schema = schema.Items
			continue
		}

		for name := range schema.Properties {
			if strings.ContainsAny(name, ".[]") {
				return nil
			}
		}
		schema = schema.Properties[part]
	}
	return plainSchema(api, schema)
}

// plainSchema resolves a generated reference and excludes combined schemas.
// Reference chains and alternative rule sets need more context than we have.
func plainSchema(api huma.API, schema *huma.Schema) *huma.Schema {
	if schema != nil && schema.Ref != "" {
		schema = api.OpenAPI().Components.Schemas.SchemaFromRef(schema.Ref)
	}
	if schema == nil || schema.Ref != "" {
		return nil
	}
	if len(schema.OneOf) > 0 || len(schema.AnyOf) > 0 || len(schema.AllOf) > 0 || schema.Not != nil {
		return nil
	}
	return schema
}
