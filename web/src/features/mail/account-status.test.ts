import { describe, expect, it } from "vite-plus/test";
import type { ListedAccount } from "@/api/generated/model";
import { listedAccount } from "@/test/mail-api";
import { accountDetail, accountStatus } from "./account-status";

// account returns an account inbok can use whose sync is sync.
function account(sync: Partial<ListedAccount["sync"]>): ListedAccount {
  const base = listedAccount(1, "ana@example.com");
  return { ...base, sync: { ...base.sync, ...sync } };
}

describe("accountStatus", () => {
  it("says nothing of an account up to date", () => {
    expect(accountStatus(listedAccount(1, "ana@example.com"))).toBeUndefined();
  });

  it("says Syncing while the first full sync waits or copies, with how far", () => {
    expect(accountStatus(account({ status: "waiting" }))).toMatchObject({
      words: "Syncing",
      tone: "info",
      detail: "Getting ready to copy the last 90 days of mail.",
    });
    expect(accountStatus(account({ status: "running", messagesSoFar: 1200 }))).toMatchObject({
      words: "Syncing",
      detail: "Copying the last 90 days: 1,200 messages so far.",
    });
  });

  it("says Sync failed, with when and what happens next", () => {
    const failed = accountStatus(
      account({ status: "failed", failure: "provider", failedAt: "2026-11-13T08:00:00Z" }),
    );
    expect(failed).toMatchObject({ words: "Sync failed", tone: "destructive" });
    expect(failed?.detail).toMatch(
      /^Couldn't get mail from Gmail at .+\. inbok will try again within an hour\.$/,
    );
  });

  it("says Reconnect for an account Google stopped accepting, before its sync", () => {
    const refused = { ...account({ status: "running" }), status: "needs-reconnect" as const };
    expect(accountStatus(refused)).toMatchObject({
      words: "Reconnect",
      tone: "warning",
      detail: "Google stopped accepting inbok's access. Reconnect it in Connected accounts.",
    });
  });
});

describe("accountDetail", () => {
  const syncing = accountStatus(account({ status: "running", messagesSoFar: 1200 }));

  it("leads with a name cut short, then the status's words and details", () => {
    expect(accountDetail("ana@work.example", true, syncing)).toBe(
      "ana@work.example. Syncing. Copying the last 90 days: 1,200 messages so far.",
    );
  });

  it("says only the status when the name shows whole, and nothing with neither", () => {
    expect(accountDetail("Work", false, syncing)).toBe(
      "Syncing. Copying the last 90 days: 1,200 messages so far.",
    );
    expect(accountDetail("Work", false, undefined)).toBe("");
  });
});
