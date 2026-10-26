import { retryAfterDelay } from "./retry-after";
import { ApiError } from "./transport";

const httpStatus = {
  firstSuccess: 200,
  lastSuccess: 299,
  requestTimeout: 408,
  tooManyRequests: 429,
  badGateway: 502,
  unavailable: 503,
  gatewayTimeout: 504,
};
const transientStatuses = new Set([
  httpStatus.requestTimeout,
  httpStatus.tooManyRequests,
  httpStatus.badGateway,
  httpStatus.unavailable,
  httpStatus.gatewayTimeout,
]);
const maxRetries = 2;
const baseDelayMs = 1_000;
const maxDelayMs = 5_000;

// Query owns retries; writes stay disabled unless an operation opts in deliberately.
export const retryOptions = {
  queries: { retry: shouldRetry, retryDelay },
  mutations: { retry: false },
};

// Only recognized temporary failures qualify for another read attempt.
function shouldRetry(failures: number, err: Error): boolean {
  if (failures >= maxRetries || !(err instanceof ApiError)) return false;
  if (err.kind === "response") return false;
  if (retryDelay(failures, err) > maxDelayMs) return false;

  const { status } = err;
  if (status === undefined) return err.kind === "network";
  if (transientStatuses.has(status)) return true;

  // An interrupted success can qualify; a rejected HTTP status keeps its meaning.
  return (
    err.kind === "network" && status >= httpStatus.firstSuccess && status <= httpStatus.lastSuccess
  );
}

// Keep normal backoff as a minimum; never shorten the server's requested delay.
function retryDelay(failures: number, err: Error): number {
  const normal = baseDelayMs * 2 ** failures;
  if (!(err instanceof ApiError)) return normal;
  if (err.status !== httpStatus.tooManyRequests && err.status !== httpStatus.unavailable)
    return normal;

  return Math.max(normal, retryAfterDelay(err.retryAfter) ?? 0);
}
