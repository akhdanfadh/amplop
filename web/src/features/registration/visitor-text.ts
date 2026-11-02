import type { ProblemType } from "@/api/generated/model";
import { problemExplanation, uncertainOutcome, userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";

const conflict = 409;
const closedType: ProblemType = "/api/v0/problems/registration-closed";

// requestedMessage answers every accepted request for a sign-up link. The
// API answers the same whether email has an account, got a notice, or got
// nothing because it asked too often, so this says no more.
export function requestedMessage(email: string): string {
  return `Check your inbox at ${email}. A sign-up link expires in 24 hours. If nothing arrives in a few minutes, check your spam folder.`;
}

// registrationClosed reports whether err refused a request because
// registration is not open, which includes paused.
export function registrationClosed(err: ApiError): boolean {
  return err.status === conflict && err.problem?.type === closedType;
}

// requestErrorMessage words a failed request for a sign-up link.
export function requestErrorMessage(err: ApiError): string {
  if (!uncertainOutcome(err)) return userMessage(err);
  // Go saves a link before sending it, so even a reported failure may deliver
  // one, and every unexpired link works.
  const explanation = problemExplanation(err);
  if (explanation) return `${explanation} If an email still arrives, its link works.`;
  // Asking again is safe: the limits bound it.
  return "We couldn't confirm whether the email was sent. If nothing arrives in a few minutes, try again.";
}
