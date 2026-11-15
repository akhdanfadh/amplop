import type { ProblemType } from "@/api/generated/model";
import { problemExplanation, uncertainMessage, uncertainOutcome, userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";

const notFound = 404;
const linkInvalidType: ProblemType = "/api/v0/problems/password-reset-link-invalid";

// requested is the answer to every accepted request for a reset link: a
// lead under "Check your email" and the address, and what to expect.
export const requested = {
  // The API answers before it looks up the address, the same for every
  // address, so this says only what happens if the address can reset.
  lead: "If this is the verified address of an active account, a reset link is on its way.",
  body: "It can take a few minutes to arrive and expires in 1 hour. If nothing arrives, check your spam folder.",
};

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

// linkInvalid reports whether err refused a reset because its link no
// longer works, which no retry with the same link can change.
export function linkInvalid(err: ApiError): boolean {
  return err.status === notFound && err.problem?.type === linkInvalidType;
}

// completeErrorMessage words a failed attempt to set a new password from a
// reset link.
export function completeErrorMessage(err: ApiError): string {
  if (!uncertainOutcome(err)) return userMessage(err);
  // A reset deletes every reset link of the account, so the next try reads
  // "not valid".
  return uncertainMessage(
    err,
    "We couldn't confirm whether your password was changed. Try again. If the link then no longer works, sign in with this new password.",
  );
}

// EarlierTries is what earlier tries on the page showed: all "refused", or
// at least one "unknown" outcome, which may have changed the password.
export type EarlierTries = "refused" | "unknown";

// EndLink is where the end of the page leads: a new link, or sign-in.
export type EndLink = "new-link" | "sign-in";

// Ended is what the page shows once its link no longer works: a title, a
// message, then links, most useful first.
export type Ended = { title: string; message: string; links: readonly EndLink[] };

// endOf returns the page's end for err, a refusal because the link no
// longer works, after earlier tries.
export function endOf(err: ApiError, earlier: EarlierTries): Ended {
  // A reset deletes the link, so an unknown try likely worked. The fields
  // may have been edited since, so this names that try, not the last one.
  if (earlier === "unknown")
    return {
      title: "Your password was probably changed",
      message:
        "This link no longer works, so an earlier try probably changed your password. Sign in with the password you sent then. If you're not sure which one that was, ask for a new link.",
      links: ["sign-in", "new-link"],
    };
  return {
    title: "This link no longer works",
    message: problemExplanation(err) ?? "This link no longer works. Ask for a new one.",
    links: ["new-link", "sign-in"],
  };
}
