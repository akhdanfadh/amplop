import { userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import { uncertainSession } from "@/features/auth/session-error";

const notFound = 404;
const conflict = 409;
const alreadyVerifiedType = "/api/v0/problems/email-already-verified";
const linkInvalidType = "/api/v0/problems/email-verification-link-invalid";

// sentMessage tells the user where a verification link went.
export function sentMessage(address: string): string {
  return `Check your inbox at ${address}. The link expires in 24 hours.`;
}

// alreadyVerified reports whether a send failed because the address is
// already verified.
export function alreadyVerified(err: ApiError): boolean {
  return err.status === conflict && err.problem?.type === alreadyVerifiedType;
}

// sendErrorMessage words a failed request for a verification link.
export function sendErrorMessage(err: ApiError): string {
  if (!uncertainSession(err)) return userMessage(err);
  // Go saves a link before sending it, so even a reported failure may deliver
  // one, and every unexpired link works.
  const explanation = err.problem?.detail?.trim() || err.problem?.title?.trim();
  if (explanation) return `${explanation} If an email still arrives, its link works.`;
  // Asking again is safe: the limit bounds it.
  return "We couldn't confirm whether the link was sent. If no email arrives in a few minutes, send another link.";
}

// linkInvalid reports whether the API refused a link as expired, used, or
// unknown.
export function linkInvalid(err: ApiError): boolean {
  return err.status === notFound && err.problem?.type === linkInvalidType;
}

// redeemErrorMessage words a failed redeem. verified is whether the
// signed-in account's address is verified, false when signed out.
export function redeemErrorMessage(err: ApiError, verified: boolean): string {
  // A used link is gone, so an account verified by it reads "not valid" on a
  // second visit.
  if (linkInvalid(err) && verified)
    return "This link is no longer valid, but your email address is already verified.";
  if (!uncertainSession(err)) return userMessage(err);
  const explanation = err.problem?.detail?.trim() || err.problem?.title?.trim();
  return [explanation, "We couldn't confirm whether your email address was verified. Try again."]
    .filter(Boolean)
    .join(" ");
}
