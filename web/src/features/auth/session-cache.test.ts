import { QueryClient } from "@tanstack/react-query";
import { linkOptions } from "@tanstack/react-router";
import { expect, it } from "vite-plus/test";
import { getGetSetupStatusQueryKey, getGetCurrentSessionQueryKey } from "@/api/generated/inbok";
import { mailHref, rememberMailHref } from "@/features/mail/places";
import { clearAccountCache, onAccountCacheClear, onAccountEndedElsewhere } from "./session-cache";

it("clears account data and credentials while retaining public setup status", async () => {
  const client = new QueryClient();
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
  const client = new QueryClient();
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
  clearAccountCache(new QueryClient());
  expect(mailHref()).toEqual({ to: "/mail/$place", params: { place: "inbox" } });
});

it("clears what else holds a member's data, until it stops", () => {
  const client = new QueryClient();
  const cleared: QueryClient[] = [];
  const stop = onAccountCacheClear((c) => cleared.push(c));
  clearAccountCache(client);
  stop();
  clearAccountCache(client);
  expect(cleared).toEqual([client]);
});

it("tells this browser's other tabs, and hears theirs", async () => {
  // Another tab's end of the channel.
  const otherTab = new BroadcastChannel("inbok-account");
  try {
    const told = new Promise((resolve) => {
      otherTab.addEventListener("message", (event) => resolve(event.data), { once: true });
    });
    clearAccountCache(new QueryClient());
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
