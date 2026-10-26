import type { ApiError } from "./transport";

// HTTP statuses (RFC 9110) that change what the user is told.
const unprocessable = 422;
const firstServerError = 500;
const badGateway = 502;
const firstSuccess = 200;
const lastSuccess = 299;
const firstClientError = 400;
const writeMethods = new Set(["POST", "PUT", "PATCH", "DELETE"]);

// The app's words for a failed request.
const cannotReach = "Can't reach the server. Try again.";
const checkValues = "Check the values and try again.";
const serverFailed = "Something went wrong on our side. Try again.";
const failed = "Something went wrong. Try again.";
const uncertainWrite = "We couldn't confirm whether this was saved. Check before submitting again.";
const unreadableWrite =
  "The server reported success, but we couldn't load the result. Check before submitting again.";

// Show safe API text and warn when repeating a write could duplicate its effect.
export function userMessage(err: ApiError): string {
  const { status } = err;
  const explanation = err.problem?.detail?.trim() || err.problem?.title?.trim();

  // A missing response or 5xx does not prove rollback. Preserve reported success too.
  if (writeMethods.has(err.method ?? "")) {
    if (
      err.kind === "response" ||
      (status !== undefined && status >= firstSuccess && status <= lastSuccess)
    ) {
      return unreadableWrite;
    }
    if (status === undefined || status >= firstServerError) {
      return [explanation, uncertainWrite].filter(Boolean).join(" ");
    }
  }
  if (explanation) return explanation;

  // A 502 comes from a proxy that could not reach Go.
  if (status === unprocessable) return checkValues;
  if (
    (err.kind === "network" && (status === undefined || status < firstClientError)) ||
    status === badGateway
  )
    return cannotReach;
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
