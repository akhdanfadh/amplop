import { describe, expect, it } from "vite-plus/test";
import type { AccountSync, ConnectedAccounts, ListedAccount } from "@/api/generated/model";
import { busyRefresh, checkLanded, idleRefresh, refreshInterval, syncView } from "./sync";

const email = "mail@example.com";
const earlier = "2026-11-11T09:00:00Z";
const later = "2026-11-11T09:00:05.123456Z";

const idle: AccountSync = {
  status: "complete",
  messagesSoFar: null,
  lastSyncedAt: earlier,
  failure: null,
  failedAt: null,
};

function account(
  sync: Partial<AccountSync>,
  status: ListedAccount["status"] = "usable",
): ListedAccount {
  return {
    id: 3,
    provider: "gmail",
    email,
    needsReconnectSince: status === "needs-reconnect" ? earlier : null,
    status,
    createdAt: earlier,
    name: null,
    color: 1,
    // As the server lists them: an unreadable token still syncs, but only a
    // usable account sends.
    allowedActions:
      status === "needs-reconnect"
        ? ["reconnect", "rename", "disconnect"]
        : status === "token-unreadable"
          ? ["sync", "reconnect", "rename", "disconnect"]
          : ["sync", "send", "reconnect", "rename", "disconnect"],
    sync: { ...idle, ...sync },
  };
}

function list(accounts: ListedAccount[]): ConnectedAccounts {
  return { accounts, syncStopped: false };
}

describe("syncView", () => {
  it("shows the first state that applies", () => {
    expect(syncView({ ...idle, status: "waiting" })).toEqual({ kind: "waiting" });
    expect(syncView({ ...idle, status: "running", messagesSoFar: 250 })).toEqual({
      kind: "running",
      messagesSoFar: 250,
    });
    expect(syncView(idle)).toEqual({ kind: "complete", checkedAt: earlier });
  });

  it("shows a failure, with when a complete account was last up to date", () => {
    const failure = { failure: "provider", failedAt: later } as const;
    expect(syncView({ ...idle, ...failure })).toEqual({
      kind: "failed",
      failedAt: later,
      failure: "provider",
      status: "complete",
      upToDateAt: earlier,
    });
    expect(syncView({ ...idle, ...failure, status: "failed" })).toMatchObject({
      kind: "failed",
      status: "failed",
      upToDateAt: null,
    });
  });
});

describe("refreshInterval", () => {
  const now = 1_000_000;

  it("reads often while a sync moves", () => {
    expect(refreshInterval(list([account({ status: "waiting" })]), now, 0)).toBe(busyRefresh);
    expect(refreshInterval(list([account({ status: "running", messagesSoFar: 1 })]), now, 0)).toBe(
      busyRefresh,
    );
  });

  it("reads often until a Check now's fast reads end", () => {
    expect(refreshInterval(list([account({})]), now, now + 1)).toBe(busyRefresh);
    expect(refreshInterval(undefined, now, now + 1)).toBe(busyRefresh);
    expect(refreshInterval(list([account({})]), now, now)).toBe(idleRefresh);
  });

  it("reads each minute otherwise", () => {
    expect(refreshInterval(undefined, now, 0)).toBe(idleRefresh);
    expect(refreshInterval(list([]), now, 0)).toBe(idleRefresh);
    expect(refreshInterval(list([account({})]), now, 0)).toBe(idleRefresh);
    // An account waiting for its member moves nothing.
    for (const status of ["needs-reconnect", "token-unreadable"] as const)
      expect(refreshInterval(list([account({ status: "waiting" }, status)]), now, 0)).toBe(
        idleRefresh,
      );
  });
});

describe("checkLanded", () => {
  it("waits until the list shows a newer check or failure", () => {
    expect(checkLanded(idle, idle)).toBe(false);
    expect(checkLanded({ ...idle, lastSyncedAt: later }, idle)).toBe(true);
    expect(checkLanded({ ...idle, failure: "provider", failedAt: later }, idle)).toBe(true);
    // An older failure, still shown, is not this check's.
    const failed = { ...idle, failure: "provider", failedAt: earlier } as const;
    expect(checkLanded(failed, failed)).toBe(false);
  });

  it("ends when a full sync takes over, whose progress the row shows", () => {
    expect(checkLanded({ ...idle, status: "waiting" }, idle)).toBe(true);
    expect(checkLanded({ ...idle, status: "running", messagesSoFar: 0 }, idle)).toBe(true);
  });
});
