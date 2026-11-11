import { describe, expect, it } from "vite-plus/test";
import type { AccountSync, ConnectedAccounts, ListedAccount } from "@/api/generated/model";
import { ApiError } from "@/api/transport";
import {
  busyRefresh,
  failureWords,
  formatMoment,
  idleRefresh,
  refreshInterval,
  runningText,
  syncErrorMessage,
  syncStartedMessage,
  syncView,
} from "./sync";

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
    sync: { ...idle, ...sync },
  };
}

function list(accounts: ListedAccount[]): ConnectedAccounts {
  return { accounts, syncStopped: false };
}

function refused(status: number, type: string): ApiError {
  return new ApiError("http", {
    method: "POST",
    status,
    problem: { type, status, title: "Refused", detail: "Refused." },
  });
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

describe("wording", () => {
  it("counts the messages copied", () => {
    expect(runningText(1)).toBe(
      "Copying your last 90 days of mail into inbok: 1 message so far. New mail shows up in the meantime.",
    );
    expect(runningText(0)).toMatch(/: 0 messages so far\./);
  });

  it("words a failure by its cause and what happens next", () => {
    expect(failureWords("provider", "complete")).toEqual({
      before: "Couldn't get mail from Gmail at",
      after: ". inbok will try again.",
    });
    expect(failureWords("provider", "failed").after).toBe(". inbok will try again within an hour.");
    for (const failure of ["token-unreadable", "internal"] as const)
      expect(failureWords(failure, "failed")).toEqual({
        before: "Couldn't get mail at",
        after: " because of a problem on inbok's side. Ask your administrator.",
      });
  });

  it("shows a time with its date only on another day", () => {
    const now = new Date(2026, 10, 11, 12, 0);
    const today = new Date(2026, 10, 11, 9, 30).toISOString();
    const yesterday = new Date(2026, 10, 10, 9, 30).toISOString();
    expect(formatMoment(today, now)).not.toMatch(/2026/);
    expect(formatMoment(yesterday, now)).toMatch(/2026/);
  });

  it("says a Check now started", () => {
    expect(syncStartedMessage(email)).toBe(`Checking ${email} for new mail.`);
  });
});

describe("syncErrorMessage", () => {
  it("words each refusal by its type", () => {
    expect(
      syncErrorMessage(refused(404, "/api/v0/problems/connected-account-not-found"), email),
    ).toBe(`${email} is no longer connected.`);
    expect(syncErrorMessage(refused(409, "/api/v0/problems/account-needs-reconnect"), email)).toBe(
      `Reconnect ${email} before checking it for mail.`,
    );
    expect(syncErrorMessage(refused(429, "/api/v0/problems/sync-throttled"), email)).toBe(
      `inbok checked ${email} just now. Try again in a few seconds.`,
    );
  });

  it("asks for a look when the outcome is unknown", () => {
    const lost = new ApiError("network", { method: "POST" });
    expect(syncErrorMessage(lost, email)).toBe(
      `Couldn't tell whether inbok started checking ${email}. See its status below.`,
    );
  });
});
