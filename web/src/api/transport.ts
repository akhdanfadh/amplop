import type { ZodError, ZodType } from "zod";
import { ReceivedProblem } from "./received-problem";

const problemType = "application/problem+json";
const requestIdHeader = "X-Request-ID";
const retryAfterHeader = "Retry-After";
const noContentStatus = 204;
// Leave room for delivery around the backend's ten-second work limit.
const requestTimeoutMs = 15_000;

export type ApiErrorKind =
  // Sending or reading failed.
  | "network"
  // Our waiting limit, or one supplied by the caller, expired.
  | "timeout"
  // A complete error response arrived.
  | "http"
  // A complete successful reply did not match its expected body.
  | "response";

// The method identifies writes even without a response. An unreadable body
// can still provide a status and request ID, even when no problem could be read.
type ErrorFacts = {
  method?: string;
  status?: number;
  problem?: ReceivedProblem;
  requestId?: string;
  retryAfter?: string;
};

// Keep the actual status separate from the problem's advisory status.
// A proxy may send HTML, leaving us with a status but no problem.
export class ApiError extends Error {
  readonly kind: ApiErrorKind;
  readonly method?: string;
  readonly status?: number;
  readonly problem?: ReceivedProblem;
  readonly requestId?: string;
  readonly retryAfter?: string;

  constructor(kind: ApiErrorKind, facts: ErrorFacts = {}) {
    super(facts.problem?.detail || facts.problem?.title || "Request failed.");
    this.name = "ApiError";
    this.kind = kind;
    this.method = facts.method;
    this.status = facts.status;
    this.problem = facts.problem;
    this.requestId = facts.requestId;
    this.retryAfter = facts.retryAfter;
  }
}

// Orval supplies a generated body type, but request failures use ApiError.
export type ErrorType<_Body> = ApiError;

// transport checks JSON responses against their generated success schema.
export function transport<T>(url: string, init: RequestInit & { schema: ZodType<T> }): Promise<T>;
// transport accepts a schema-less 204 response for bodyless operations.
export function transport<T extends void>(
  url: string,
  init: RequestInit & { schema?: undefined },
): Promise<T>;
export async function transport(
  url: string,
  init: RequestInit & { schema?: ZodType },
): Promise<unknown> {
  // schema is our validation setting, not a fetch option.
  const { schema, ...rest } = init;
  const facts: ErrorFacts = { method: init.method?.toUpperCase() ?? "GET" };

  // One timer covers sending and body reading. Clear it after reading or a
  // failure; the caller can still cancel an obsolete attempt sooner.
  const deadline = new AbortController();
  const signal = init.signal ? AbortSignal.any([init.signal, deadline.signal]) : deadline.signal;
  const timer = setTimeout(() => {
    deadline.abort(new DOMException("Request timed out.", "TimeoutError"));
  }, requestTimeoutMs);

  let res: Response;
  let text: string;
  try {
    // fetch resolves for HTTP errors too; a rejected call has no response facts.
    res = await fetch(url, { ...rest, signal });

    // Headers arrive before the body finishes. Save them now so a later read
    // failure keeps the actual status and request reference.
    facts.status = res.status;
    facts.requestId = res.headers.get(requestIdHeader) || undefined;
    facts.retryAfter = res.headers.get(retryAfterHeader) ?? undefined;
    text = res.status === noContentStatus ? "" : await res.text();
  } catch (err) {
    if (isTimeout(signal)) throw new ApiError("timeout", facts);
    if (signal.aborted) throw err;
    throw new ApiError("network", facts);
  } finally {
    clearTimeout(timer);
  }

  let body: unknown;
  try {
    body = JSON.parse(text);
  } catch {
    // The body arrived completely. Invalid JSON keeps the HTTP status; a
    // successful status with this body will fail the success schema below.
    body = undefined;
  }
  // An error status stays an HTTP failure even if its body is HTML or invalid.
  // Parse only the declared problem media type; never invent a problem.
  if (!res.ok) {
    const contentType = res.headers.get("Content-Type")?.split(";", 1)[0]?.trim().toLowerCase();
    if (contentType === problemType) {
      const result = ReceivedProblem.safeParse(body);
      if (result.success) facts.problem = result.data;
    }
    throw new ApiError("http", facts);
  }

  // Orval omits the schema for bodyless operations; accept only 204 there.
  if (!schema || res.status === noContentStatus) {
    if (!schema && res.status === noContentStatus) return undefined;
    logMismatch(
      url,
      schema ? "Expected a response body." : "Expected a 204 response without a body.",
    );
    throw new ApiError("response", facts);
  }

  // A success status alone is not enough: callers need the promised data.
  // An incompatible body is a response failure, not interrupted communication.
  const result = schema.safeParse(body);
  if (result.success) return result.data;
  logMismatch(url, result.error);
  throw new ApiError("response", facts);
}

// Recognize a deadline from either our timer or the caller's signal.
function isTimeout(signal: AbortSignal): boolean {
  const reason: unknown = signal.reason;
  return (
    signal.aborted &&
    typeof reason === "object" &&
    reason !== null &&
    "name" in reason &&
    reason.name === "TimeoutError"
  );
}

// Contract mismatches belong in developer diagnostics; screens choose user text.
function logMismatch(url: string, err: ZodError | string): void {
  console.error(`${url}: response does not match the API contract`, err);
}
