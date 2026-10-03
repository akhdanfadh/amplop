import type { ApiError } from "./transport";

// "Not Found: route not found", or the title alone when there is no detail.
export function summarize(err: ApiError) {
  const { title, detail } = err.problem;
  return [title, detail].filter(Boolean).join(": ");
}
