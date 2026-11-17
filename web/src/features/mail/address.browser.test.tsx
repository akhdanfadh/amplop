import { QueryObserver, onlineManager } from "@tanstack/react-query";
import { describe, expect, it } from "vite-plus/test";
import { appRouter } from "@/test/app-router";
import {
  getListConnectedAccountsQueryKey,
  getListConnectedAccountsQueryOptions,
} from "@/api/generated/inbok";
import { refreshMail } from "@/features/mail/conversations";
import { conversation, listedAccount, mailApi, type MailApi } from "@/test/mail-api";

const label = "11111111-1111-4111-8111-111111111111";

// twoAccounts returns a MailApi with two accounts, a label of the first,
// and one conversation.
function twoAccounts(): MailApi {
  const api = mailApi(
    [conversation(1)],
    [listedAccount(1, "ana@example.com"), listedAccount(2, "ana@work.example")],
  );
  api.places = [{ accountId: 1, name: "Receipts", parent: null, place: label, unread: 0 }];
  return api;
}

// open returns the app's router at address, its mail reads answered by api.
async function open(address: string, api = twoAccounts()) {
  return appRouter(address, api.server);
}

// notFound reports whether the router shows Page not found.
function notFound(router: Awaited<ReturnType<typeof open>>): boolean {
  return router.state.matches.some((match) => match.status === "notFound");
}

describe("mail addresses", () => {
  it("opens a view, a label by its ID, and one account's list", async () => {
    for (const address of ["/mail/inbox", `/mail/${label}`, "/mail/all?account=2"]) {
      // oxlint-disable-next-line eslint/no-await-in-loop -- one page at a time.
      const router = await open(address);
      expect({ address, notFound: notFound(router) }).toEqual({ address, notFound: false });
    }
  });

  it("shows every account's list for an account the member doesn't have", async () => {
    const router = await open("/mail/inbox?account=9");
    expect(router.state.location.pathname).toBe("/mail/inbox");
    expect(router.state.location.search).toEqual({});
    expect(notFound(router)).toBe(false);
  });

  it("opens a label in its own account, filtered to another or to none", async () => {
    for (const address of [`/mail/${label}?account=2`, `/mail/${label}`]) {
      // oxlint-disable-next-line eslint/no-await-in-loop -- one page at a time.
      const router = await open(address);
      expect({ address, search: router.state.location.search }).toEqual({
        address,
        search: { account: 1 },
      });
      expect(notFound(router)).toBe(false);
    }
  });

  it("checks a filter against accounts read again once a write marked them out of date", async () => {
    const api = twoAccounts();
    const router = await open("/mail/inbox?account=2", api);
    // An account connected since; the write only marks the cached list, as
    // it does while no open page shows the list.
    api.accounts.push(listedAccount(3, "ana@home.example"));
    await router.options.context.queryClient.invalidateQueries({
      queryKey: getListConnectedAccountsQueryKey(),
      refetchType: "none",
    });

    await router.navigate({
      to: "/mail/$place",
      params: { place: "inbox" },
      search: { account: 3 },
    });

    // Not dropped as an account the member doesn't have.
    expect(router.state.location.search).toEqual({ account: 3 });
  });

  it("checks a filter again on a visit after a preload, once a write marked the accounts", async () => {
    const api = twoAccounts();
    const router = await open("/mail/inbox", api);
    await router.preloadRoute({
      to: "/mail/$place",
      params: { place: "inbox" },
      search: { account: 2 },
    });
    // The account is disconnected after the preload, e.g. on hover.
    api.accounts = api.accounts.filter((a) => a.id !== 2);
    await router.options.context.queryClient.invalidateQueries({
      queryKey: getListConnectedAccountsQueryKey(),
      refetchType: "none",
    });

    await router.navigate({
      to: "/mail/$place",
      params: { place: "inbox" },
      search: { account: 2 },
    });

    // Dropped as an account the member no longer has, not kept from the preload.
    expect(router.state.location.search).toEqual({});
  });

  it("checks a filter against the accounts it has while offline, without waiting", async () => {
    const router = await open("/mail/inbox?account=2");
    await router.options.context.queryClient.invalidateQueries({
      queryKey: getListConnectedAccountsQueryKey(),
      refetchType: "none",
    });
    onlineManager.setOnline(false);

    // Not awaited: a read waiting for the connection would never settle.
    void router.navigate({
      to: "/mail/$place",
      params: { place: "inbox" },
      search: { account: 1 },
    });

    // The router's location changes as the visit starts; the resolved one
    // only once its loaders have settled.
    await expect.poll(() => router.state.resolvedLocation?.search).toEqual({ account: 1 });
  });

  it("reads the accounts a page shows again on a refresh, marking none out of date", async () => {
    const api = twoAccounts();
    const router = await open("/mail/inbox", api);
    const client = router.options.context.queryClient;
    // The accounts as the sidebar uses them; Infinity, so it sends no read
    // of its own as it starts.
    const shown = new QueryObserver(client, {
      ...getListConnectedAccountsQueryOptions(),
      staleTime: Infinity,
    });
    const stop = shown.subscribe(() => undefined);
    try {
      await expect.poll(() => shown.getCurrentResult().isSuccess).toBe(true);
      api.accounts.push(listedAccount(3, "ana@home.example"));

      await refreshMail(client);

      expect(shown.getCurrentResult().data?.data.accounts.map((a) => a.id)).toEqual([1, 2, 3]);
      // The cache's own mark, which no screen shows: a query left out of
      // date would be read once more by the next page that uses it.
      expect(client.getQueryState(getListConnectedAccountsQueryKey())?.isInvalidated).toBe(false);
    } finally {
      stop();
    }
  });

  it("leaves accounts no page shows unmarked on a refresh", async () => {
    const router = await open("/mail/inbox?account=2");
    const client = router.options.context.queryClient;

    await refreshMail(client);

    expect(client.getQueryState(getListConnectedAccountsQueryKey())?.isInvalidated).toBe(false);
  });

  it("drops an account that is no ID", async () => {
    const router = await open("/mail/inbox?account=abc");
    expect(router.state.matches.at(-1)?.search).toEqual({ account: undefined });
    expect(notFound(router)).toBe(false);
  });

  it("is Page not found for a label or conversation that does not exist", async () => {
    for (const address of [
      "/mail/22222222-2222-4222-8222-222222222222",
      "/mail/nope",
      "/mail/inbox/22222222-2222-4222-8222-222222222222",
      "/mail/inbox/nope",
    ]) {
      // oxlint-disable-next-line eslint/no-await-in-loop -- one page at a time.
      const router = await open(address);
      expect({ address, notFound: notFound(router) }).toEqual({ address, notFound: true });
    }
  });

  it("opens a conversation it reads by its ID", async () => {
    const router = await open(`/mail/inbox/${conversation(1).id}`);
    expect(notFound(router)).toBe(false);
    expect(router.state.matches.at(-1)?.routeId).toBe("/_app/_mail/mail/$place/$conversationId");
  });

  it("keeps the conversation when it drops an account filter", async () => {
    const id = conversation(1).id;
    const router = await open(`/mail/inbox/${id}?account=9`);
    expect(router.state.location.pathname).toBe(`/mail/inbox/${id}`);
    expect(router.state.location.search).toEqual({});
  });
});
