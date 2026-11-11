import type {
  AccountSync,
  ConnectedAccounts,
  ListedAccount,
  ProblemType,
} from "@/api/generated/model";
import { uncertainMessage, uncertainOutcome, userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";

// How often the list is read again: often while a sync moves, so its
// progress and outcome show, else each minute, so the checked time stays
// current (ADR001, "Polling").
export const busyRefresh = 2_000;
export const idleRefresh = 60_000;
// How long after a Check now the list is read often, inbok's choice: a check
// that finds a few messages ends within seconds, so its time and mail show.
export const syncNowRefresh = 30_000;

const notFound = 404;
const conflict = 409;
const tooManyRequests = 429;
const accountNotFoundType: ProblemType = "/api/v0/problems/connected-account-not-found";
const needsReconnectType: ProblemType = "/api/v0/problems/account-needs-reconnect";
const throttledType: ProblemType = "/api/v0/problems/sync-throttled";
const sameDay = new Intl.DateTimeFormat(undefined, { timeStyle: "short" });
const otherDay = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });
const count = new Intl.NumberFormat();

// SyncFailure is why an account's last sync failed.
export type SyncFailure = NonNullable<AccountSync["failure"]>;

// FailedStatus is the status of an account whose last sync failed.
export type FailedStatus = Extract<AccountSync["status"], "complete" | "failed">;

// SyncView is what an account's row shows about its sync, the first that
// applies: waiting for its first full sync, copying, a failure, or up to
// date.
export type SyncView =
  | { kind: "waiting" }
  | { kind: "running"; messagesSoFar: number }
  | {
      kind: "failed";
      failedAt: string;
      failure: SyncFailure;
      // complete for a failed check of a copy that is current, failed for
      // a failed full sync, which inbok tries again an hour later.
      status: FailedStatus;
      // When a complete account's mail was last up to date; null for a
      // failed full sync, which has no current copy.
      upToDateAt: string | null;
    }
  | { kind: "complete"; checkedAt: string | null };

// syncView returns what sync's row shows.
export function syncView(sync: AccountSync): SyncView {
  if (sync.status === "waiting") return { kind: "waiting" };
  if (sync.status === "running") return { kind: "running", messagesSoFar: sync.messagesSoFar ?? 0 };
  if (sync.failure !== null && sync.failedAt !== null) {
    const complete = sync.status === "complete";
    return {
      kind: "failed",
      failedAt: sync.failedAt,
      failure: sync.failure,
      status: complete ? "complete" : "failed",
      upToDateAt: complete ? sync.lastSyncedAt : null,
    };
  }
  return { kind: "complete", checkedAt: sync.lastSyncedAt };
}

// syncBusy reports whether account's sync is moving, so the list is read
// again soon: one inbok cannot use waits for its member.
function syncBusy(account: ListedAccount): boolean {
  if (account.status !== "usable") return false;
  const { kind } = syncView(account.sync);
  return kind === "waiting" || kind === "running";
}

// refreshInterval returns how long until list is read again: busyRefresh
// while a sync moves or before fastUntil, the time in milliseconds since
// the epoch that the last Check now's fast reads end, else idleRefresh.
export function refreshInterval(
  list: ConnectedAccounts | undefined,
  now: number,
  fastUntil: number,
): number {
  if (now < fastUntil) return busyRefresh;
  if (!list) return idleRefresh;
  return list.accounts.some(syncBusy) ? busyRefresh : idleRefresh;
}

// formatMoment shows iso as a clock time in the reader's locale and time
// zone, with its date unless it falls on the same day as now.
export function formatMoment(iso: string, now: Date = new Date()): string {
  const at = new Date(iso);
  return at.toDateString() === now.toDateString() ? sameDay.format(at) : otherDay.format(at);
}

// runningText says how far a running full sync has copied.
export function runningText(messagesSoFar: number): string {
  const messages = messagesSoFar === 1 ? "message" : "messages";
  return `Copying your last 90 days of mail into inbok: ${count.format(messagesSoFar)} ${messages} so far. New mail shows up in the meantime.`;
}

// FailureWords are the words around a failure's time: before it, and
// after it, with why when it is inbok's, and what happens next.
export type FailureWords = { before: string; after: string };

// failureWords returns the words that tell the member about failure on an
// account with status, in the member's terms: getting their mail.
export function failureWords(failure: SyncFailure, status: FailedStatus): FailureWords {
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
  if (err.status === tooManyRequests && type === throttledType)
    return `inbok checked ${email} just now. Try again in a few seconds.`;
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
