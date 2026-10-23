import type { ApiError } from "./transport";

// HTTP statuses (RFC 9110) that change what the user is told.
const unprocessable = 422;
const firstServerError = 500;
const badGateway = 502;

// The app's words for a failed request.
const cannotReach = "Can't reach the server. Try again.";
const checkValues = "Check the values and try again.";
const serverFailed = "Something went wrong on our side. Try again.";
const failed = "Something went wrong. Try again.";

// Prefer safe API text, including unfamiliar problem types; otherwise use HTTP facts.
export function userMessage(err: ApiError): string {
  const explanation = err.problem?.detail?.trim() || err.problem?.title?.trim();
  if (explanation) return explanation;

  const { status } = err;

  // A 502 comes from a proxy that could not reach Go.
  if (err.kind === "network" || status === badGateway) return cannotReach;
  if (status === unprocessable) return checkValues;
  if (status !== undefined && status >= firstServerError) return serverFailed;
  return failed;
}

const bodyPrefix = "body.";
const rejected = "This value was not accepted.";

type InputMessage = { message: string };
type FormErrors = {
  fields: Record<string, InputMessage[]>;
  unmatched: InputMessage[];
};

// Match only rendered fields; errors for other inputs stay visible in the summary.
export function formErrors(err: ApiError, fieldNames: readonly string[]): FormErrors {
  const fields = new Map<string, InputMessage[]>();
  const unmatched: InputMessage[] = [];

  for (const { location, message } of err.problem?.errors ?? []) {
    const input = { message: message.trim() || rejected };
    const name = location.startsWith(bodyPrefix) ? location.slice(bodyPrefix.length) : undefined;
    if (!name || !fieldNames.includes(name)) {
      unmatched.push(input);
      continue;
    }

    const messages = fields.get(name) ?? [];
    messages.push(input);
    fields.set(name, messages);
  }

  return { fields: Object.fromEntries(fields), unmatched };
}
