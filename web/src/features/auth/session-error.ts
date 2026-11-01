import type { ApiError } from "@/api/transport";
import { uncertainMessage, uncertainOutcome, userMessage } from "@/api/problem";

// sessionMessage describes authentication failures and how to resolve an unknown outcome.
export function sessionMessage(err: ApiError, action: "sign-in" | "sign-out"): string {
  if (!uncertainOutcome(err)) return userMessage(err);
  return uncertainMessage(
    err,
    `Could not confirm ${action}. Check your session before trying again.`,
  );
}
