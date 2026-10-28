import type { ApiError } from "@/api/transport";
import { userMessage } from "@/api/problem";

const firstSuccess = 200;
const lastSuccess = 299;
const firstServerError = 500;

// uncertainSession identifies failures that do not establish the session's state.
export function uncertainSession(err: ApiError): boolean {
  return (
    err.kind === "response" ||
    err.status === undefined ||
    err.status >= firstServerError ||
    (err.status >= firstSuccess && err.status <= lastSuccess)
  );
}

// sessionMessage describes authentication failures and how to resolve an unknown outcome.
export function sessionMessage(err: ApiError, action: "sign-in" | "sign-out"): string {
  if (!uncertainSession(err)) return userMessage(err);
  const explanation = err.problem?.detail?.trim() || err.problem?.title?.trim();
  return [explanation, `Could not confirm ${action}. Check your session before trying again.`]
    .filter(Boolean)
    .join(" ");
}
