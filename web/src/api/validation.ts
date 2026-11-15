import * as z from "zod";

// The pattern the contract uses for "not blank", e.g. NewProbeEvent.note.
const notBlank = String(/\S/);
// The pattern emailAddress adds to an address field.
const addressPattern = z.regexes.unicodeEmail;
// The server's text for an address it refuses (invalidEmailMessage in
// backend/internal/httpapi/problem.go).
const invalidEmail = "Enter an email address, like name@example.com.";

// Number formats that allow only whole numbers, e.g. z.int()'s "safeint".
const wholeFormats = new Set(["safeint", "int32", "uint32"]);

// The app's words for a value that breaks a schema rule. Zod's own text
// is for developers, e.g. "Too small: expected number to be >=1". Each
// message states the field's whole rule, e.g. "Enter a whole number from
// 1 to 10.", so one fix is enough.
export function validationMessage(iss: z.core.$ZodRawIssue): string {
  if (iss.code === "invalid_format" && iss.pattern === notBlank) return "Can't be blank.";
  if (iss.code === "invalid_format" && iss.pattern === String(addressPattern)) return invalidEmail;

  const { schema } = iss;
  if (schema instanceof z.ZodNumber) return numberRule(schema);

  const badLength = iss.code === "too_small" || iss.code === "too_big";
  if (schema instanceof z.ZodString && badLength) return lengthRule(schema);
  if (schema instanceof z.ZodArray && iss.code === "too_big") return itemsRule(iss.maximum);

  return "Enter a valid value.";
}

// emailAddress returns schema, a contract string of format "idn-email",
// after a check that the value has an @ with text without spaces or quotes
// on both sides, so a form refuses a mistyped address before sending it.
// A value that fails that check gets its one message and skips schema's
// own checks, e.g. an empty field reads "Enter an email address, like
// name@example.com." without "Must be 3 to 254 characters." too.
//
// DESIGN: the server decides what an address is (Go's net/mail and inbok's
// own checks), so this rule is never stricter than the server's, except
// that it allows at most 64 characters before the @, the most RFC 5321
// allows (section 4.5.3.1.1). Orval generates no check for "idn-email",
// so each form's schema adds this one.
export function emailAddress(schema: z.ZodString): z.ZodPipe<z.ZodString, z.ZodString> {
  // A pipe stops at the first schema's issues (Zod's handlePipeResult), so
  // the length limits speak only for an address that is otherwise one.
  return z.string().regex(addressPattern).pipe(schema);
}

// "Enter a whole number from 1 to 10.", or with one limit or none.
function numberRule(schema: z.ZodNumber): string {
  const whole = schema.format !== null && wholeFormats.has(schema.format);
  const kind = whole ? "a whole number" : "a number";
  const min = limit(schema.minValue);
  const max = limit(schema.maxValue);

  if (min !== undefined && max !== undefined) return `Enter ${kind} from ${min} to ${max}.`;
  if (min !== undefined) return `Enter ${kind} no less than ${min}.`;
  if (max !== undefined) return `Enter ${kind} no more than ${max}.`;
  return `Enter ${kind}.`;
}

// "Must be 1 to 40 characters.", or with one limit.
function lengthRule(schema: z.ZodString): string {
  const { minLength: min, maxLength: max } = schema;

  if (min !== null && max !== null) return `Must be ${min} to ${characters(max)}.`;
  if (min !== null) return `Must be at least ${characters(min)}.`;
  if (max !== null) return `Must be at most ${characters(max)}.`;
  return "Enter a valid value.";
}

// "Must have at most 100 items.", the rule the contract's maxItems sets.
// Zod keeps no list limits on the schema, so the refused issue gives it.
function itemsRule(max: number | bigint): string {
  return max === 1 ? "Must have at most 1 item." : `Must have at most ${max} items.`;
}

// Zod reports "no limit" as null, Infinity, or the largest safe integer.
function limit(value: number | null): number | undefined {
  if (value === null || Math.abs(value) >= Number.MAX_SAFE_INTEGER) return undefined;
  return value;
}

// "1 character", "120 characters".
function characters(count: number): string {
  return count === 1 ? "1 character" : `${count} characters`;
}
