import type { ZodError, ZodType } from "zod";
import { ReceivedProblem } from "./received-problem";

const problemType = "application/problem+json";
const requestIdHeader = "X-Request-ID";
const retryAfterHeader = "Retry-After";

export type ApiErrorKind =
  // Sending or reading failed.
  | "network"
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

// Generated API calls pass their success schema here. Return checked data
// or throw an error containing the response facts received so far.
export async function transport<T>(
  url: string,
  init: RequestInit & { schema?: ZodType<T> },
): Promise<T> {
  // schema is our validation setting, not a fetch option. Missing it is a
  // programming error; reject it before sending a request.
  const { schema, ...rest } = init;
  if (!schema) throw new Error(`${url}: no response schema`);
  const facts: ErrorFacts = { method: init.method?.toUpperCase() ?? "GET" };

  // fetch resolves for HTTP errors too; a rejected call has no response facts.
  let res: Response;
  try {
    res = await fetch(url, rest);
  } catch (err) {
    if (isCancelled(init.signal)) throw err;
    throw new ApiError("network", facts);
  }

  // Headers arrive before the body finishes. Save them now so a later read
  // failure keeps the actual status and request reference.
  facts.status = res.status;
  facts.requestId = res.headers.get(requestIdHeader) || undefined;
  facts.retryAfter = res.headers.get(retryAfterHeader) ?? undefined;
  let text: string;
  try {
    text = await res.text();
  } catch (err) {
    if (isCancelled(init.signal)) throw err;
    throw new ApiError("network", facts);
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

  // A success status alone is not enough: callers need the promised data.
  // An incompatible body is a response failure, not interrupted communication.
  const result = schema.safeParse(body);
  if (result.success) return result.data;
  logMismatch(url, result.error);
  throw new ApiError("response", facts);
}

// Preserve deliberate cancellation so obsolete requests are not shown as failures.
// A signal aborted with TimeoutError still represents a failed request.
function isCancelled(signal: AbortSignal | null | undefined): boolean {
  if (!signal?.aborted) return false;
  const reason: unknown = signal.reason;
  return !(
    typeof reason === "object" &&
    reason !== null &&
    "name" in reason &&
    reason.name === "TimeoutError"
  );
}

// Contract mismatches belong in developer diagnostics; screens choose user text.
function logMismatch(url: string, err: ZodError): void {
  console.error(`${url}: response does not match the API contract`, err);
}
