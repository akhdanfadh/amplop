import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { appRouter, memberSession } from "@/test/app-router";
import { conversation, listedAccount, mailApi } from "@/test/mail-api";

const label = "11111111-1111-4111-8111-111111111111";

afterEach(() => {
  vi.unstubAllGlobals();
});

// open returns the app's router at address, with two accounts, a label of
// the first, and one conversation.
async function open(address: string) {
  const api = mailApi(
    [conversation(1)],
    [listedAccount(1, "ana@example.com"), listedAccount(2, "ana@work.example")],
  );
  api.places = [{ accountId: 1, name: "Receipts", parent: null, place: label, unread: 0 }];
  return appRouter(address, memberSession, (url) =>
    new URL(url, location.href).pathname === "/api/v0/sessions/current"
      ? undefined
      : api.fetch(url),
  );
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

  it("shows a label whole when filtered to another account", async () => {
    const router = await open(`/mail/${label}?account=2`);
    expect(router.state.location.search).toEqual({});
    expect(notFound(router)).toBe(false);
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
