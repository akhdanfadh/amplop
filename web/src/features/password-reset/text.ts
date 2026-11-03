import { uncertainMessage, uncertainOutcome, userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";

// requestedMessage returns the answer to every accepted request for a reset
// link to email.
export function requestedMessage(email: string): string {
  // The API answers before it looks up email, the same for every address, so
  // this says only what happens if the address can reset.
  return `If ${email} is the verified address of an active account, a reset link is on its way. It can take a few minutes to arrive and expires in 1 hour. If nothing arrives, check your spam folder.`;
}

// requestErrorMessage words a failed request for a reset link.
export function requestErrorMessage(err: ApiError): string {
  if (!uncertainOutcome(err)) return userMessage(err);
  // Asking again is safe: earlier links keep working, and the limits bound
  // how many arrive.
  return uncertainMessage(
    err,
    "We couldn't confirm whether your request went through. If no email arrives in a few minutes, try again.",
  );
}
