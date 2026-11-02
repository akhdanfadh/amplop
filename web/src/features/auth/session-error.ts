import type { ApiError } from "@/api/transport";
import { uncertainMessage, uncertainOutcome, userMessage } from "@/api/problem";

const unauthorized = 401;
const forbidden = 403;

// sessionMessage describes authentication failures and how to resolve an unknown outcome.
export function sessionMessage(err: ApiError, action: "sign-in" | "sign-out"): string {
  if (!uncertainOutcome(err)) return userMessage(err);
  return uncertainMessage(
    err,
    `Could not confirm ${action}. Check your session before trying again.`,
  );
}

// challengeEnded reports whether err from the code step ends the sign-in
// attempt, so that only starting again can continue: a 401 or a 403.
export function challengeEnded(err: ApiError): boolean {
  // A 401 is an expired or used challenge. A 403 is mostly an account
  // deactivated after its password was accepted, but a refused request
  // source is one too; starting again is the way on from either.
  return err.status === unauthorized || err.status === forbidden;
}
