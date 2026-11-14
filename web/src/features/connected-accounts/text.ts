import type { ConnectOutcome, ProblemType } from "@/api/generated/model";
import { uncertainMessage, uncertainOutcome, userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import type { LeaveWarningText } from "@/components/leave-warning";
import { formatCount } from "@/lib/count";
import type { FailedStatus, SyncFailure } from "./sync";

const notFound = 404;
const conflict = 409;
const tooManyRequests = 429;
const accountNotFoundType: ProblemType = "/api/v0/problems/connected-account-not-found";
const needsReconnectType: ProblemType = "/api/v0/problems/account-needs-reconnect";
const throttledType: ProblemType = "/api/v0/problems/sync-throttled";
const shownDate = new Intl.DateTimeFormat(undefined, { dateStyle: "medium" });
const sameDay = new Intl.DateTimeFormat(undefined, { timeStyle: "short" });
const otherDay = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });

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

// What an account's row says about its sync, and its sync button's labels.
export const syncStoppedText =
  "inbok has stopped getting new mail for every account. Ask your administrator.";
export const syncWaitingText = "Getting ready to copy your mail.";
export const upToDateText = "Up to date.";
// The words before the time of the last check.
export const checkedAtText = "Up to date, checked at";
// The words before the time a complete account's mail was last up to date.
export const upToDateAsOfText = "Your mail here is up to date as of";
export const checkNowText = "Check now";
export const tryAgainText = "Try again";
export const checkingText = "Checking...";

// formatMoment shows iso as a clock time in the reader's locale and time
// zone, with its date unless it falls on the same day as now.
export function formatMoment(iso: string, now: Date = new Date()): string {
  const at = new Date(iso);
  return at.toDateString() === now.toDateString() ? sameDay.format(at) : otherDay.format(at);
}

// runningText says how far a running full sync has copied.
export function runningText(messagesSoFar: number): string {
  const messages = messagesSoFar === 1 ? "message" : "messages";
  return `Copying your last 90 days of mail into inbok: ${formatCount(messagesSoFar)} ${messages} so far. New mail shows up in the meantime.`;
}

// failureWords returns the words around the time of failure on an account
// with status, in the member's terms, getting their mail: before the time,
// and after it, with why when it is inbok's, and what happens next.
export function failureWords(
  failure: SyncFailure,
  status: FailedStatus,
): { before: string; after: string } {
  if (failure !== "provider")
    return {
      before: "Couldn't get mail at",
      after: " because of a problem on inbok's side. Ask your administrator.",
    };
  const retry = status === "failed" ? " within an hour" : "";
  return { before: "Couldn't get mail from Gmail at", after: `. inbok will try again${retry}.` };
}

// syncErrorMessage describes a Check now of email that the server refused
// or whose outcome is unknown.
export function syncErrorMessage(err: ApiError, email: string): string {
  // Only the API's own problem types say why; a missing route says nothing.
  const type = err.problem?.type;
  if (err.status === notFound && type === accountNotFoundType)
    return `${email} is no longer connected.`;
  if (err.status === conflict && type === needsReconnectType)
    return `Reconnect ${email} before checking it for mail.`;
  // A refusal here means a check started moments ago, which the status
  // below follows, so it says what is happening rather than when to retry:
  // there is nothing to retry.
  if (err.status === tooManyRequests && type === throttledType)
    return `inbok started checking ${email} moments ago. See its status below.`;
  if (uncertainOutcome(err))
    return uncertainMessage(
      err,
      `Couldn't tell whether inbok started checking ${email}. See its status below.`,
    );
  return `Couldn't check ${email} for mail. ${userMessage(err)}`;
}

// syncStartedMessage says a Check now of email was accepted.
export function syncStartedMessage(email: string): string {
  return `Checking ${email} for new mail.`;
}

// The account name field (DESIGN.md "Sidebar").
export const nameLabel = "Name (optional)";
// Private, unlike the sender name Gmail's "Send mail as" gives recipients.
export const nameDescription =
  "A name only you see, shown in mail instead of the address, e.g. Work. Leave it empty to show the address.";
export const savedText = "Saved";
export const saveNameText = "Save name";
export const savingNameText = "Saving...";
export const nameLeaveWarning: LeaveWarningText = {
  title: "Leave without saving the name?",
  description: "The name you typed isn't saved.",
  stay: "Keep editing",
  leave: "Leave",
  leaveVariant: "destructive",
};
