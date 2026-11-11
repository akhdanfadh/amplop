import { retryAfterTime } from "./retry-after";
import type { ApiError } from "./transport";

// ===============================================
// Request messages
// ===============================================

// HTTP statuses (RFC 9110) that change what the user is told.
const unprocessable = 422;
const tooManyRequests = 429;
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
const tooMany = "Too many attempts.";
const tryLater = "Try again later.";

const millisecondsPerMinute = 60_000;
// A weekday names one day only within the next six.
const longestNamedWait = 6 * 24 * 60 * millisecondsPerMinute;
// The browser's locale chooses 12- or 24-hour time.
const clockTime = new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" });
const weekday = new Intl.DateTimeFormat(undefined, { weekday: "long" });

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
// duplicate its effect. A 429 also says when to try again.
export function userMessage(err: ApiError): string {
  const { status } = err;

  if (uncertainOutcome(err)) {
    // Preserve reported success: the server said the write happened; only its
    // result is lost.
    if (err.kind === "response" || reportedSuccess(status)) return unreadableWrite;
    return uncertainMessage(err, uncertainWrite);
  }
  const explanation = problemExplanation(err);
  if (status === tooManyRequests) return `${sentence(explanation) ?? tooMany} ${retryAdvice(err)}`;
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

// retryAdvice says when err's request can be tried again, from its
// Retry-After header, as a local clock time rounded up to the minute: with
// the weekday when that is not the day the response arrived. A time already
// past counts from when the response arrived. Without a usable header, or
// for a wait over six days, it says "later".
//
// DESIGN: a clock time stays true while the message stays on screen, which a
// duration such as "in 3 minutes" does not; the weekday, unlike "tomorrow",
// stays true past midnight. It is worked out from when the response arrived,
// so every render shows the same text.
function retryAdvice(err: ApiError): string {
  if (err.receivedAt === undefined) return tryLater;
  const time = retryAfterTime(err.retryAfter, err.receivedAt);
  // Received data can hold any wait; one too long for a weekday, or for a
  // Date, says "later" instead of an ambiguous or invalid time.
  if (time === undefined || !(time - err.receivedAt <= longestNamedWait)) return tryLater;
  // A past HTTP date would otherwise name a day already gone.
  const earliest = Math.max(time, err.receivedAt);
  // Rounding down would invite a retry the server still refuses.
  const at = new Date(Math.ceil(earliest / millisecondsPerMinute) * millisecondsPerMinute);
  const clock = clockTime.format(at);
  // Some locales end the time with a full stop, e.g. "2:32 p.m.".
  if (sameDay(at, new Date(err.receivedAt))) return sentence(`Try again after ${clock}`);
  return `Try again after ${clock} on ${weekday.format(at)}.`;
}

// sameDay reports whether a and b fall on the same local calendar day.
function sameDay(a: Date, b: Date): boolean {
  return (
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate()
  );
}

// sentence returns text ending in a full stop, adding one when it has no
// final punctuation, e.g. after a problem title; undefined stays undefined.
function sentence(text: string): string;
function sentence(text: string | undefined): string | undefined;
function sentence(text: string | undefined): string | undefined {
  if (text === undefined || /[.!?]$/.test(text)) return text;
  return `${text}.`;
}

// ===============================================
// Field error mapping
// ===============================================

const bodyPrefix = "body.";
const rejected = "This value was not accepted.";

type InputMessage = { message: string };
export type FormErrors = {
  fields: Record<string, InputMessage[]>;
  unmatched: InputMessage[];
};

// Match only rendered fields; errors for other inputs stay visible in the summary.
// An item of a list a form renders as one field, e.g. body.to[3] for a
// field "to", marks that field.
export function formErrors(err: ApiError, fieldNames: readonly string[]): FormErrors {
  const fields = new Map<string, InputMessage[]>();
  const unmatched: InputMessage[] = [];

  for (const { location, message } of err.problem?.errors ?? []) {
    const input = { message: message.trim() || rejected };
    const path = location.startsWith(bodyPrefix) ? location.slice(bodyPrefix.length) : undefined;
    const name = path === undefined ? undefined : renderedField(path, fieldNames);
    if (!name) {
      unmatched.push(input);
      continue;
    }

    const messages = fields.get(name) ?? [];
    messages.push(input);
    fields.set(name, messages);
  }

  return { fields: Object.fromEntries(fields), unmatched };
}

// renderedField returns the field in fieldNames that path names: path
// itself, or the list holding the item at path. It returns undefined when
// no rendered field holds path.
function renderedField(path: string, fieldNames: readonly string[]): string | undefined {
  if (fieldNames.includes(path)) return path;
  return fieldNames.find((name) => path.startsWith(`${name}[`));
}

// formSummary returns the messages for the summary under a form's button
// after a failed request: message, the request's own text, then the input
// errors in errors that match no rendered field. It returns none when every
// input error matched a field.
export function formSummary(message: string, errors: FormErrors): InputMessage[] {
  // The fields then hold the whole refusal and focus moves to the first; a
  // request reference is for reporting a fault, which a refused value is not.
  if (Object.keys(errors.fields).length > 0 && errors.unmatched.length === 0) return [];
  return [{ message }, ...errors.unmatched];
}
