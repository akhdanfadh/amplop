import type { AccountSync, ConnectedAccounts, ListedAccount } from "@/api/generated/model";

// How often the list is read again: often while a sync moves, so its
// progress and outcome show, else each minute, so the checked time stays
// current.
export const busyRefresh = 2_000;
export const idleRefresh = 60_000;
// How long after a Check now the list is read often, amplop's choice: a check
// that finds a few messages ends within seconds, so its time and mail show.
export const syncNowRefresh = 30_000;

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
      // a failed full sync, which amplop tries again an hour later.
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
// again soon: one amplop cannot use waits for its member.
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

// checkLanded reports whether sync shows the outcome of a Check now
// accepted while the account's sync was before: a check or failure newer
// than before's, or a full sync, whose progress the row then shows.
export function checkLanded(sync: AccountSync, before: AccountSync): boolean {
  if (sync.status === "waiting" || sync.status === "running") return true;
  return later(sync.lastSyncedAt, before.lastSyncedAt) || later(sync.failedAt, before.failedAt);
}

// later reports whether the time at is after since; null is never.
function later(at: string | null, since: string | null): boolean {
  return at !== null && (since === null || Date.parse(at) > Date.parse(since));
}
