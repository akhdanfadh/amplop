import type { ApiError } from "./transport";

// ===============================================
// Request messages
// ===============================================

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
const timedOut = "The request took too long. Try again.";
const checkValues = "Check the values and try again.";
const serverFailed = "Something went wrong on our side. Try again.";
const failed = "Something went wrong. Try again.";
const uncertainWrite = "We couldn't confirm whether this was saved. Check before submitting again.";
const unreadableWrite =
  "The server reported success, but we couldn't load the result. Check before submitting again.";

// uncertainOutcome reports whether err leaves it unknown whether its write
// took effect: a write that got no status, a 5xx, or a success whose result
// could not be read. Reads and received rejections are never uncertain; an
// error without a method counts as a write.
export function uncertainOutcome(err: ApiError): boolean {
  // An unknown method might be a write, so it counts as one.
  if (err.method !== undefined && !writeMethods.has(err.method)) return false;
  // Neither a missing response, a 5xx, nor an unreadable success shows the
  // write was rolled back.
  return (
    err.kind === "response" ||
    err.status === undefined ||
    err.status >= firstServerError ||
    reportedSuccess(err.status)
  );
}

// problemExplanation returns the received problem's detail, else its title,
// trimmed, or undefined when neither has text.
export function problemExplanation(err: ApiError): string | undefined {
  return err.problem?.detail?.trim() || err.problem?.title?.trim() || undefined;
}

// uncertainMessage returns the received problem's explanation, if any,
// followed by advice on what to check before trying again.
export function uncertainMessage(err: ApiError, advice: string): string {
  return [problemExplanation(err), advice].filter(Boolean).join(" ");
}

// reportedSuccess reports whether status is a 2xx.
function reportedSuccess(status: number | undefined): boolean {
  return status !== undefined && status >= firstSuccess && status <= lastSuccess;
}

// userMessage returns safe text for err, warning when repeating a write could
// duplicate its effect.
export function userMessage(err: ApiError): string {
  const { status } = err;

  if (uncertainOutcome(err)) {
    // Preserve reported success: the server said the write happened; only its
    // result is lost.
    if (err.kind === "response" || reportedSuccess(status)) return unreadableWrite;
    return uncertainMessage(err, uncertainWrite);
  }
  const explanation = problemExplanation(err);
  if (explanation) return explanation;

  if (status === unprocessable) return checkValues;
  if (err.kind === "timeout" && (status === undefined || status < firstClientError))
    return timedOut;

  // A 502 comes from a proxy that could not reach Go.
  if (
    (err.kind === "network" && (status === undefined || status < firstClientError)) ||
    status === badGateway
  )
    return cannotReach;
  if (status !== undefined && status >= firstServerError) return serverFailed;
  return failed;
}

// ===============================================
// Field error mapping
// ===============================================

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
