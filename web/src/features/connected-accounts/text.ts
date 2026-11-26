import type { ConnectOutcome, ProblemType } from "@/api/generated/model";
import { uncertainMessage, uncertainOutcome, userMessage } from "@/api/problem";
import { retryAfterTime } from "@/api/retry-after";
import type { ApiError } from "@/api/transport";
import type { NoticeTone } from "@/features/settings/page";
import { formatCount } from "@/lib/count";
import type { FailedStatus, SyncFailure } from "./sync";
import { clockTime, dayAndTime, dayDate } from "@/lib/datetime";

const notFound = 404;
const conflict = 409;
const tooManyRequests = 429;
// The longest wait "in a few seconds" describes; a longer one gets the
// clock time DESIGN.md "Forms" asks for.
const fewSecondsMs = 60_000;
export const accountNotFoundType: ProblemType = "/api/v0/problems/connected-account-not-found";
export const needsReconnectType: ProblemType = "/api/v0/problems/account-needs-reconnect";
export const throttledType: ProblemType = "/api/v0/problems/sync-throttled";
export const gmailNotConfiguredType: ProblemType = "/api/v0/problems/gmail-not-configured";

// PageNotice is what the page says under its lead about the last thing
// that happened, in its tone (SettingsPage).
export type PageNotice = { tone: NoticeTone; text: string };

// A problem means the member has something to fix; success and neutral
// report what happened, neutral when nothing changed.
const outcomeNotices: Record<ConnectOutcome, PageNotice> = {
  connected: { tone: "success", text: "Your Gmail account is connected." },
  cancelled: {
    tone: "neutral",
    text: "Connecting was cancelled on Google's page, so nothing changed.",
  },
  "gmail-not-granted": {
    tone: "problem",
    text: "Gmail wasn't connected because access to Gmail was left unticked on Google's page. Connect again and tick it.",
  },
  "email-not-verified": {
    tone: "problem",
    text: "Google hasn't verified that account's email address, so it can't be connected. Choose another account, or verify the address with Google first.",
  },
  "no-gmail": {
    tone: "problem",
    text: "That Google account doesn't use Gmail, so it can't be connected. Choose an account that does.",
  },
  "already-connected": { tone: "neutral", text: "You've already connected that Gmail account." },
  "different-account": {
    tone: "problem",
    text: "You chose a different Google account from the one you were reconnecting. Reconnect again and choose the same account.",
  },
  expired: {
    tone: "problem",
    text: "That connection attempt expired or was already used. Start again.",
  },
  failed: { tone: "problem", text: "Something went wrong while connecting Gmail. Try again." },
};

// outcomeNotice returns what the page says about outcome, the way Google's
// return to amplop ended.
export function outcomeNotice(outcome: ConnectOutcome): PageNotice {
  return outcomeNotices[outcome];
}

// The page's lead, its one section, and the Gmail-less instance.
export const pageLead = "The Gmail accounts amplop gets your mail from and sends it with.";
export const sectionTitle = "Gmail accounts";
export const sectionAbout = "Each account's mail shows in your inbox, under its name.";
export const listReadFailed = "Couldn't refresh this page, so it may be out of date.";
export const connectText = "Connect Gmail";
export const openingGoogleText = "Opening Google...";
export const reconnectText = "Reconnect";
export const disconnectText = "Disconnect";
export const emptyTitle = "Connect your first account";
export const emptyText = "amplop shows the mail of the accounts you connect.";
// While no Google client is set up, by who can fix it: the empty state's
// title and next step, and the sentence under the list where Connect Gmail
// would be. The title says what can't happen, not what is missing, so a
// member doesn't take it for their own Gmail.
export const notOfferedTitle = "Gmail can't be connected yet";
export const notOfferedStep = {
  member: "Ask your administrator to set up Gmail on this amplop.",
  admin:
    'Add a Google client to this amplop, as the self-hosting guide\'s "Connecting Gmail" explains.',
};
export const notOfferedText = {
  member:
    "Your administrator hasn't set up Gmail on this amplop, so accounts can't be connected or reconnected. Ask them to set it up.",
  admin:
    "Gmail isn't set up on this amplop, so accounts can't be connected or reconnected. Add a Google client, as the self-hosting guide's \"Connecting Gmail\" explains.",
};

// formatDate shows the calendar date of iso as amplop writes one, in the
// reader's time zone.
export function formatDate(iso: string): string {
  return dayDate.format(new Date(iso));
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

// The disconnect dialog's title, around the account's name.
export const disconnectTitle = { before: "Disconnect ", after: "?" };

// disconnectWords are the disconnect dialog's for the account named name:
// its button and that button's label while it waits, and its body, which
// says amplop can't end Google's access when unreadable, i.e. the saved
// permission can no longer be read.
export function disconnectWords(
  name: string,
  unreadable: boolean,
): { action: string; working: string; description: string } {
  return {
    action: `Disconnect ${name}`,
    working: "Disconnecting...",
    description: `${
      unreadable
        ? "amplop removes this account and everything it saved for it. It can't ask Google to end its access, so you can remove amplop in your Google Account."
        : "amplop removes this account and everything it saved for it, and asks Google to end its access."
    } Your mail in Gmail stays as it is.`,
  };
}

// disconnectedNotice reports a disconnect the server confirmed for the
// account named name.
export function disconnectedNotice(name: string): PageNotice {
  return { tone: "success", text: `${name} is disconnected.` };
}

// isAccountGone reports whether err is the API's own answer that the
// account no longer exists; a missing route says nothing.
export function isAccountGone(err: ApiError): boolean {
  return err.status === notFound && err.problem?.type === accountNotFoundType;
}

// alreadyDisconnectedNotice reports that the account named name was gone
// before the member's disconnect reached it, e.g. from another tab.
export function alreadyDisconnectedNotice(name: string): PageNotice {
  return { tone: "neutral", text: `${name} was already disconnected.` };
}

// disconnectFailure words a failed disconnect of the account named name,
// shown in the dialog, where pressing again retries. An unknown outcome
// asks for a look at the list, which is read again after every disconnect.
export function disconnectFailure(err: ApiError, name: string): string {
  if (!uncertainOutcome(err)) return `Couldn't disconnect ${name}. ${userMessage(err)}`;
  return uncertainMessage(
    err,
    `Couldn't confirm whether ${name} is disconnected. Check the list before trying again.`,
  );
}

// The banner on every signed-in page while syncing has stopped on this
// amplop, e.g. as no worker runs: its landmark name, and its words by who
// can fix it.
export const syncStoppedText = {
  label: "Mail sync stopped",
  member: "amplop has stopped getting new mail for every account. Ask your administrator.",
  admin: "amplop has stopped getting new mail for every account. Check that its worker is running.",
};

// What an account's row says about its sync, and its sync button's labels.
export const syncWaitingText = "Getting ready to copy your mail.";
export const upToDateText = "Up to date.";
// The words before the time of the last check.
export const checkedAtText = "Up to date, checked at";
// The words before the time a complete account's mail was last up to date.
export const upToDateAsOfText = "Your mail here is up to date as of";
export const checkNowText = "Check now";
export const tryAgainText = "Try again";
export const checkingText = "Checking...";

// formatMoment shows iso as a clock time as amplop writes one, in the
// reader's time zone, with its date unless it falls on the same day as now.
export function formatMoment(iso: string, now: Date = new Date()): string {
  const at = new Date(iso);
  return at.toDateString() === now.toDateString() ? clockTime.format(at) : dayAndTime.format(at);
}

// runningText says how far a running full sync has copied.
export function runningText(messagesSoFar: number): string {
  const messages = messagesSoFar === 1 ? "message" : "messages";
  return `Copying your last 90 days of mail into amplop: ${formatCount(messagesSoFar)} ${messages} so far. New mail shows up in the meantime.`;
}

// failureWords returns the words around the time of failure on an account
// with status, in the member's terms, getting their mail: before the time,
// and after it, with why when it is amplop's, and what happens next.
export function failureWords(
  failure: SyncFailure,
  status: FailedStatus,
): { before: string; after: string } {
  if (failure !== "provider")
    return {
      before: "Couldn't get mail at",
      after: " because of a problem on amplop's side. Ask your administrator.",
    };
  const retry = status === "failed" ? " within an hour" : "";
  return { before: "Couldn't get mail from Gmail at", after: `. amplop will try again${retry}.` };
}

// syncNotice says why a Check now of the account named name was refused,
// or that its outcome is unknown.
export function syncNotice(err: ApiError, name: string): PageNotice {
  // Only the API's own problem types say why; a missing route says nothing.
  const type = err.problem?.type;
  if (err.status === notFound && type === accountNotFoundType)
    return { tone: "neutral", text: `${name} is no longer connected.` };
  if (err.status === conflict && type === needsReconnectType)
    return { tone: "problem", text: `Reconnect ${name} before checking it for mail.` };
  // The server's wait, from Retry-After, decides the words: under a
  // minute is too short for a clock time (DESIGN.md "Forms"); a longer or
  // unreadable one gets the usual time to try again.
  if (err.status === tooManyRequests && type === throttledType) {
    const received = err.receivedAt ?? Date.now();
    const until = retryAfterTime(err.retryAfter, received);
    if (until !== undefined && until - received <= fewSecondsMs)
      return {
        tone: "neutral",
        text: `${name} was checked moments ago. Check again in a few seconds.`,
      };
    // Named, as the short wait is: several rows can be checked.
    return { tone: "neutral", text: `${name}: ${userMessage(err)}` };
  }
  // Not a problem to fix: the every-minute check gets the mail anyway.
  if (uncertainOutcome(err))
    return {
      tone: "neutral",
      text: uncertainMessage(
        err,
        `Couldn't tell whether amplop started checking ${name}. Its mail still arrives within a minute.`,
      ),
    };
  return { tone: "problem", text: `Couldn't check ${name} for mail. ${userMessage(err)}` };
}

// The account name: its pencil's tooltip, and its field (DESIGN.md
// "Sidebar").
export const renameText = "Rename";
export const nameLabel = "Name";
// Private, unlike the sender name Gmail's "Send mail as" gives recipients.
export const nameDescription =
  "A name only you see, shown in mail instead of the address, e.g. Work. Leave it empty to show the address.";
export const saveText = "Save";
export const savingText = "Saving...";
export const cancelText = "Cancel";
