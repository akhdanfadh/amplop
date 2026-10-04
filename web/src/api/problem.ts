import type { ApiError } from "./transport";

// "Not Found: route not found", or the title alone when there is no detail.
export function summarize(err: ApiError) {
  const { title, detail } = err.problem;
  return [title, detail].filter(Boolean).join(": ");
}

// HTTP statuses (RFC 9110) that change what the user is told.
const unprocessable = 422;
const firstServerError = 500;
const badGateway = 502;

// The app's words for a failed request.
const cannotReach = "Can't reach the server. Try again.";
const checkValues = "Check the values and try again.";
const serverFailed = "Something went wrong on our side. Try again.";
const failed = "Something went wrong. Try again.";

// What to tell the user about a failed request. Go's text is for
// developers, e.g. "Unprocessable Entity"; a form may word a status it
// expects better, e.g. 409 "The counter is full.".
export function userMessage(err: ApiError) {
  const { status } = err.problem;

  // A 502 comes from a proxy that could not reach Go.
  if (err.kind === "unreachable" || status === badGateway) return cannotReach;
  if (status === unprocessable) return checkValues;
  if (status !== undefined && status >= firstServerError) return serverFailed;
  return failed;
}

// Go names a rejected request field by where it sits, e.g. "body.note".
const bodyPrefix = "body.";

// Shaped like a Zod issue, so a form shows it like its own errors.
const rejected = { message: "This value was not accepted." };

// Go's per-field errors, keyed by form field name, e.g. "body.note" is
// "note" and "body.items[3].tags" is "items[3].tags". Go's own message is
// developer text, and the form's rule must be outdated if Go disagrees,
// so each field gets one plain message. Errors outside the body's fields,
// e.g. a missing field reported at "body", are left to userMessage.
export function fieldErrors(err: ApiError): Record<string, typeof rejected> {
  const fields: Record<string, typeof rejected> = {};
  for (const { location } of err.problem.errors ?? []) {
    if (!location?.startsWith(bodyPrefix)) continue;
    fields[location.slice(bodyPrefix.length)] = rejected;
  }
  return fields;
}
