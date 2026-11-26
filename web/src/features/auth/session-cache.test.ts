import type { QueryClient } from "@tanstack/react-query";
import { testQueryClient } from "@/test/query-client";
import { linkOptions } from "@tanstack/react-router";
import { expect, it } from "vite-plus/test";
import { getGetSetupStatusQueryKey, getGetCurrentSessionQueryKey } from "@/api/generated/amplop";
import { mailHref, rememberMailHref } from "@/features/mail/places";
import {
  clearAccountCache,
  clearTabAccountData,
  endTabAccount,
  onAccountCacheClear,
  onAccountEndedElsewhere,
} from "./session-cache";

it("clears account data and credentials while retaining public setup status", async () => {
  const client = testQueryClient();
  const setup = { data: { status: "done" } };
  client.setQueryData(getGetSetupStatusQueryKey(), setup);
  client.setQueryData(getGetCurrentSessionQueryKey(), { data: { email: "old@example.com" } });
  client.setQueryData(["private-messages"], ["old account data"]);
  const mutation = client.getMutationCache().build(client, {
    mutationFn: async (_credentials: { password: string }) => undefined,
  });
  await mutation.execute({ password: "test-only-password" });

  clearAccountCache(client);

  expect(client.getQueryData(getGetSetupStatusQueryKey())).toEqual(setup);
  expect(client.getQueryData(getGetCurrentSessionQueryKey())).toBeUndefined();
  expect(client.getQueryData(["private-messages"])).toBeUndefined();
  expect(client.getMutationCache().getAll()).toHaveLength(0);
  client.clear();
});

it("cancels an outstanding account read so it cannot repopulate the cache", async () => {
  const client = testQueryClient();
  let requestSignal: AbortSignal | undefined;
  const request = client
    .query({
      queryKey: ["private-messages"],
      queryFn: ({ signal }) => {
        requestSignal = signal;
        return new Promise<string[]>(() => {});
      },
    })
    .catch(() => undefined);

  clearAccountCache(client);
  await request;

  expect(requestSignal?.aborted).toBe(true);
  expect(client.getQueryData(["private-messages"])).toBeUndefined();
  client.clear();
});

it("sends Back to mail to the Inbox, so the next member never gets the last one's page", () => {
  rememberMailHref(linkOptions({ to: "/mail/outbox/$id", params: { id: "a" } }));
  clearAccountCache(testQueryClient());
  expect(mailHref()).toEqual({ to: "/mail/$place", params: { place: "inbox" } });
});

it("clears what else holds a member's data, until it stops", () => {
  const client = testQueryClient();
  const cleared: QueryClient[] = [];
  const stop = onAccountCacheClear((c) => cleared.push(c));
  clearAccountCache(client);
  stop();
  clearAccountCache(client);
  expect(cleared).toEqual([client]);
});

it("tells this browser's other tabs, and hears theirs", async () => {
  // Another tab's end of the channel.
  const otherTab = new BroadcastChannel("amplop-account");
  try {
    const told = new Promise((resolve) => {
      otherTab.addEventListener("message", (event) => resolve(event.data), { once: true });
    });
    clearAccountCache(testQueryClient());
    expect(await told).toBe("ended");

    const heard = new Promise<void>((resolve) => {
      const stop = onAccountEndedElsewhere(() => {
        stop();
        resolve();
      });
    });
    // oxlint-disable-next-line unicorn/require-post-message-target-origin -- a BroadcastChannel takes no origin.
    otherTab.postMessage("ended");
    await heard;
  } finally {
    otherTab.close();
  }
});

it("clears this tab alone for another tab's end, telling no tab back", async () => {
  const otherTab = new BroadcastChannel("amplop-account");
  // A third tab, which sends the marker below.
  const thirdTab = new BroadcastChannel("amplop-account");
  try {
    const told: unknown[] = [];
    otherTab.addEventListener("message", (event) => told.push(event.data));
    const client = testQueryClient();
    client.setQueryData(["account-data"], 1);
    const cleared: QueryClient[] = [];
    const stop = onAccountCacheClear((cleared_) => cleared.push(cleared_));

    clearTabAccountData(client);
    stop();
    expect(client.getQueryData(["account-data"])).toBeUndefined();
    expect(cleared).toEqual([client]);
    // A channel hands over its messages in the order they were sent, so
    // one sent before this marker would be heard before it.
    const marked = new Promise((resolve) => {
      otherTab.addEventListener("message", resolve, { once: true });
    });
    // oxlint-disable-next-line unicorn/require-post-message-target-origin -- a BroadcastChannel takes no origin.
    thirdTab.postMessage("marker");
    await marked;
    expect(told).toEqual(["marker"]);
  } finally {
    otherTab.close();
    thirdTab.close();
  }
});

it("clears this tab before reloading it for another tab's end", () => {
  const client = testQueryClient();
  client.setQueryData(["account-data"], 1);
  const seen: string[] = [];
  const stop = onAccountCacheClear(() => seen.push("cleared"));

  // The reload is the page's own; what matters is that clearing comes first,
  // so a leave warning that cancels the reload keeps nothing.
  endTabAccount(client, () => {
    seen.push(client.getQueryData(["account-data"]) === undefined ? "reload, cleared" : "reload");
  });
  stop();
  expect(seen).toEqual(["cleared", "reload, cleared"]);
});
