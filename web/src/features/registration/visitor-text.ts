import type { ProblemType } from "@/api/generated/model";
import { problemExplanation, uncertainMessage, uncertainOutcome, userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";

const notFound = 404;
const conflict = 409;
const closedType: ProblemType = "/api/v0/problems/registration-closed";
const linkInvalidType: ProblemType = "/api/v0/problems/registration-link-invalid";
const accountExistsType: ProblemType = "/api/v0/problems/registration-account-exists";

// CompleteEnd is a refused completion that no retry with the same link can
// change: "link-invalid" or "account-exists".
export type CompleteEnd = "link-invalid" | "account-exists";

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

// completeEnd returns why err ends the page, or undefined when another try
// may work, e.g. after registration reopens.
export function completeEnd(err: ApiError): CompleteEnd | undefined {
  const type = err.problem?.type;
  if (err.status === notFound && type === linkInvalidType) return "link-invalid";
  if (err.status === conflict && type === accountExistsType) return "account-exists";
  return undefined;
}

// completeErrorMessage words a failed attempt to create an account from a
// sign-up link.
export function completeErrorMessage(err: ApiError): string {
  if (!uncertainOutcome(err)) return userMessage(err);
  // A created account deletes its link, so the next try reads "not valid".
  return uncertainMessage(
    err,
    "We couldn't confirm whether your account was created. Try again. If the link is then no longer valid, sign in with this password.",
  );
}

// EarlierTries is what earlier tries on a page showed: all "refused", or at
// least one "unknown" outcome, which may have created the account.
export type EarlierTries = "refused" | "unknown";

// EndLink is where the end of the page leads: a new link, or sign-in.
export type EndLink = "new-link" | "sign-in";

// Ended is what the page shows once no retry with its link can work: a
// message, then links, most useful first.
export type Ended = { message: string; links: readonly EndLink[] };

// endOf returns the page's end for end, the refusal err carries, after
// earlier tries.
export function endOf(err: ApiError, end: CompleteEnd, earlier: EarlierTries): Ended {
  if (end === "account-exists") return { message: completeErrorMessage(err), links: ["sign-in"] };
  // A created account deletes its link, so the unknown try likely worked.
  if (earlier === "unknown")
    return {
      message:
        "This link no longer works, so your last try probably created your account. Sign in with the password you chose.",
      links: ["sign-in", "new-link"],
    };
  return { message: completeErrorMessage(err), links: ["new-link", "sign-in"] };
}
