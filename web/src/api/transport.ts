import type { ErrorModel } from "./generated/inbok";

// Content type of a Problem Details body (RFC 9457), Go's error format.
const problemType = "application/problem+json";

// A failed API call. `problem` is Go's Problem Details body, or a stand-in
// with the same fields when the failure never reached Go.
export class ApiError extends Error {
  readonly problem: ErrorModel;

  constructor(problem: ErrorModel) {
    super(problem.title);
    this.name = "ApiError";
    this.problem = problem;
  }
}

// Sends every generated operation's request (see orval.config.ts).
// Returns the success body; a failed request throws ApiError.
export async function transport<T>(url: string, init: RequestInit): Promise<T> {
  const res = await send(url, init);
  if (!res.ok) throw await failure(res);

  const body: unknown = await res.json();
  // oxlint-disable-next-line typescript/no-unsafe-type-assertion -- trusted until response validation
  return body as T;
}

// fetch throws only when no response arrives, e.g. offline. A request the
// caller cancelled keeps its own error, since nothing failed.
async function send(url: string, init: RequestInit): Promise<Response> {
  try {
    return await fetch(url, init);
  } catch (err) {
    if (init.signal?.aborted) throw err;
    throw new ApiError({ title: "Could not reach the server" });
  }
}

// Go's Problem Details, or a stand-in for any other body, e.g. the dev
// proxy's plain-text 502 while Go is down.
async function failure(res: Response): Promise<ApiError> {
  const type = res.headers.get("Content-Type") ?? "";
  if (!type.startsWith(problemType)) {
    return new ApiError({ status: res.status, title: res.statusText });
  }

  const body: unknown = await res.json();
  // oxlint-disable-next-line typescript/no-unsafe-type-assertion -- trusted until response validation
  return new ApiError(body as ErrorModel);
}
