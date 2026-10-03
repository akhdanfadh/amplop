import type { ZodError, ZodType } from "zod";
import { ErrorModel } from "./generated/model";

// Content type of a Problem Details body (RFC 9457), Go's error format.
const problemType = "application/problem+json";

// Why a request failed, so the app can choose what to tell the user.
export type ApiErrorKind =
  // No response arrived, e.g. offline.
  | "unreachable"
  // An error status, e.g. Go's 409, or the dev proxy's 502 while Go is down.
  | "status"
  // A body that breaks the API contract, e.g. an HTML page.
  | "unexpected";

// A failed API call. `problem` is Go's Problem Details body, or a stand-in
// with the same fields when Go sent none.
export class ApiError extends Error {
  readonly kind: ApiErrorKind;
  readonly problem: ErrorModel;

  constructor(kind: ApiErrorKind, problem: ErrorModel) {
    super(problem.title);
    this.name = "ApiError";
    this.kind = kind;
    this.problem = problem;
  }
}

// The error type of every generated hook, e.g. useGetHealth().error.
// Orval passes the documented error body, but transport throws ApiError for
// every failed request.
export type ErrorType<_Body> = ApiError;

// Sends every generated operation's request (see orval.config.ts).
// Returns the success body; a failed request throws ApiError.
export async function transport<T>(
  url: string,
  init: RequestInit & { schema?: ZodType<T> },
): Promise<T> {
  // Orval omits the schema only for a body it cannot describe, e.g. none
  // (204 No Content). No operation has one yet.
  const { schema, ...rest } = init;
  if (!schema) throw new Error(`${url}: no response schema`);

  const res = await send(url, rest);
  if (!res.ok) throw await failure(url, res);

  const result = await parse(res, schema);
  if (result.success) return result.data;

  // E.g. an HTML page, or a field Go renamed without `pnpm generate`.
  logMismatch(url, result.error);
  throw new ApiError("unexpected", { title: "Unexpected response from the server" });
}

// fetch throws only when no response arrives, e.g. offline. A request the
// caller cancelled keeps its own error, since nothing failed.
async function send(url: string, init: RequestInit): Promise<Response> {
  try {
    return await fetch(url, init);
  } catch (err) {
    if (init.signal?.aborted) throw err;
    throw new ApiError("unreachable", { title: "Could not reach the server" });
  }
}

// Go's Problem Details, or a stand-in for any other body, e.g. the dev
// proxy's plain-text 502 while Go is down.
async function failure(url: string, res: Response): Promise<ApiError> {
  const standIn = { status: res.status, title: res.statusText };
  const type = res.headers.get("Content-Type") ?? "";
  if (!type.startsWith(problemType)) return new ApiError("status", standIn);

  const result = await parse(res, ErrorModel);
  if (result.success) return new ApiError("status", result.data);

  logMismatch(url, result.error);
  return new ApiError("status", standIn);
}

// Checks the body against schema. A body that is not JSON, e.g. an HTML
// page, fails like JSON of the wrong shape.
async function parse<T>(res: Response, schema: ZodType<T>) {
  const body: unknown = await res.json().catch((err: unknown) => {
    if (err instanceof SyntaxError) return undefined;
    throw err;
  });
  return schema.safeParse(body);
}

// Tells the developer which fields broke the contract; the user sees only
// the error's title.
function logMismatch(url: string, err: ZodError) {
  console.error(`${url}: response does not match the API contract`, err);
}
