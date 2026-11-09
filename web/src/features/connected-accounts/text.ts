import type { ConnectOutcome, ProblemType } from "@/api/generated/model";
import { uncertainMessage, uncertainOutcome, userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";

const notFound = 404;
const accountNotFoundType: ProblemType = "/api/v0/problems/connected-account-not-found";
const shownDate = new Intl.DateTimeFormat(undefined, { dateStyle: "medium" });

// OutcomeNotice is what the page says about how a connection ended, as a
// polite notice or an interrupting alert.
type OutcomeNotice = { kind: "notice" | "alert"; text: string };

// An alert means the member has something to fix; a notice reports what
// happened.
const outcomeNotices: Record<ConnectOutcome, OutcomeNotice> = {
  connected: { kind: "notice", text: "Your Gmail account is connected." },
  cancelled: {
    kind: "notice",
    text: "Connecting was cancelled on Google's page, so nothing changed.",
  },
  "gmail-not-granted": {
    kind: "alert",
    text: "Gmail wasn't connected because access to Gmail was left unticked on Google's page. Connect again and tick it.",
  },
  "email-not-verified": {
    kind: "alert",
    text: "Google hasn't verified that account's email address, so it can't be connected. Choose another account, or verify the address with Google first.",
  },
  "no-gmail": {
    kind: "alert",
    text: "That Google account doesn't use Gmail, so it can't be connected. Choose an account that does.",
  },
  "already-connected": { kind: "notice", text: "You've already connected that Gmail account." },
  "different-account": {
    kind: "alert",
    text: "You chose a different Google account from the one you were reconnecting. Reconnect again and choose the same account.",
  },
  expired: {
    kind: "alert",
    text: "That connection attempt expired or was already used. Start again.",
  },
  failed: { kind: "alert", text: "Something went wrong while connecting Gmail. Try again." },
};

// outcomeNotice returns what the page says about outcome, the way Google's
// return to inbok ended.
export function outcomeNotice(outcome: ConnectOutcome): OutcomeNotice {
  return outcomeNotices[outcome];
}

// formatDate shows the calendar date of iso in the reader's locale and time zone.
export function formatDate(iso: string): string {
  return shownDate.format(new Date(iso));
}

// startErrorMessage describes a connect or reconnect that could not open
// Google's page.
export function startErrorMessage(err: ApiError): string {
  // Only the API's own 404 says the account is gone; a missing route says nothing.
  if (err.status === notFound && err.problem?.type === accountNotFoundType)
    return "That account is no longer connected, so it can't be reconnected.";
  // An unanswered start saves nothing the member must check: at worst an
  // attempt that is never finished and expires.
  if (uncertainOutcome(err))
    return uncertainMessage(err, "Could not open Google's page. Try again.");
  return userMessage(err);
}

// disconnectedMessage reports a disconnect the server confirmed for email.
export function disconnectedMessage(email: string): string {
  return `${email} is disconnected.`;
}

// disconnectErrorMessage describes a failed disconnect of email. An unknown
// outcome asks for a look at the list, which reloads after every disconnect.
export function disconnectErrorMessage(err: ApiError, email: string): string {
  // Only the API's own 404 says the account is gone; a missing route says nothing.
  if (err.status === notFound && err.problem?.type === accountNotFoundType)
    return `${email} was already disconnected.`;
  if (!uncertainOutcome(err)) return `Could not disconnect ${email}. ${userMessage(err)}`;
  return uncertainMessage(
    err,
    `Could not confirm whether ${email} is disconnected. Check the list before trying again.`,
  );
}
