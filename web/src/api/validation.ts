import * as z from "zod";

// The pattern the contract uses for "not blank", e.g. NewProbeEvent.note.
const notBlank = String(/\S/);

// Number formats that allow only whole numbers, e.g. z.int()'s "safeint".
const wholeFormats = new Set(["safeint", "int32", "uint32"]);

// The app's words for a value that breaks a schema rule. Zod's own text
// is for developers, e.g. "Too small: expected number to be >=1". Each
// message states the field's whole rule, e.g. "Enter a whole number from
// 1 to 10.", so one fix is enough.
export function validationMessage(iss: z.core.$ZodRawIssue): string {
  if (iss.code === "invalid_format" && iss.pattern === notBlank) return "Can't be blank.";

  const { schema } = iss;
  if (schema instanceof z.ZodNumber) return numberRule(schema);

  const badLength = iss.code === "too_small" || iss.code === "too_big";
  if (schema instanceof z.ZodString && badLength) return lengthRule(schema);

  return "Enter a valid value.";
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

// Zod reports "no limit" as null, Infinity, or the largest safe integer.
function limit(value: number | null): number | undefined {
  if (value === null || Math.abs(value) >= Number.MAX_SAFE_INTEGER) return undefined;
  return value;
}

// "1 character", "120 characters".
function characters(count: number): string {
  return count === 1 ? "1 character" : `${count} characters`;
}
