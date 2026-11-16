import { QueryClient } from "@tanstack/react-query";
import { createLiveQueryCollection } from "@tanstack/react-db";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { getListPlacesQueryKey } from "@/api/generated/inbok";
import type { PlaceCount } from "@/api/generated/model";
import { clearAccountCache } from "@/features/auth/session-cache";
import { conversation, listedAccount, mailApi, type MailApi } from "@/test/mail-api";
import { runAction, type ActionMail } from "./actions";
import { mailData, placeKey, refreshNewest, toConversation } from "./conversations";
import { placeWhere } from "./rules";

let current: QueryClient | undefined;

afterEach(() => {
  if (current) clearAccountCache(current);
  current = undefined;
  vi.unstubAllGlobals();
});

const inbox: PlaceCount = { accountId: 1, name: null, parent: null, place: "inbox", unread: 1 };

// openInbox serves api and returns the mail of a client with an open
// Inbox list and the places read, as a mail page has them.
async function openInbox(api: MailApi): Promise<ActionMail> {
  vi.stubGlobal("fetch", vi.fn(api.fetch));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  current = client;
  const { conversations, placeCounts } = mailData(client);
  const list = createLiveQueryCollection((q) =>
    q
      .from({ c: conversations })
      .where(({ c }) => placeWhere(c, "inbox", undefined))
      .orderBy(({ c }) => c.dateMs, "desc")
      .limit(50),
  );
  list.subscribeChanges(() => {});
  placeCounts.subscribeChanges(() => {});
  await Promise.all([list.preload(), placeCounts.preload()]);
  return { client, conversations, placeCounts };
}

describe("runAction", () => {
  it("keeps the answer over a list's refresh that read the conversation before it", async () => {
    const row = conversation(1, { unread: true });
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    const mail = await openInbox(api);
    // The refresh reads the conversation unread, and answers once let go.
    let answerRefresh: (() => void) | undefined;
    const fetch = api.fetch;
    api.fetch = async (input, init) => {
      const url = input instanceof Request ? input.url : String(input);
      if (!url.includes("/conversations?")) return fetch(input, init);
      const before = Response.json({ data: { conversations: [{ ...row, unread: true }] } });
      return new Promise((done) => {
        answerRefresh = () => done(before);
      });
    };
    vi.stubGlobal("fetch", vi.fn(api.fetch));
    const refreshed = refreshNewest(mail.client);

    await runAction(mail, { action: "read", ids: [row.id] });
    answerRefresh?.();
    await refreshed;

    expect(mail.conversations.get(row.id)?.unread).toBe(false);
  });

  it("answers a later change by its own request, though an earlier one on the row failed", async () => {
    const [work, home] = [
      "00000000-0000-4000-8000-0000000000c1",
      "00000000-0000-4000-8000-0000000000c2",
    ];
    const row = conversation(1, { labelIds: [work, home] });
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    const mail = await openInbox(api);
    api.failAction = 500;

    // Two label chips' × pressed at once: the second waits for the first.
    const first = runAction(mail, { changes: [{ ids: [row.id], remove: [work] }] });
    const second = runAction(mail, { changes: [{ ids: [row.id], remove: [home] }] });

    await expect(first).rejects.toMatchObject({ status: 500 });
    const answer = await second;
    expect(answer.refused).toEqual([]);
    expect(api.changes).toHaveLength(2);
    expect(mail.conversations.get(row.id)?.labelIds).toEqual([work]);
  });

  it("keeps the answer's counts", async () => {
    const row = conversation(1, { unread: true });
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    api.places = [inbox];
    api.placesAfter = [{ ...inbox, unread: 0 }];
    const mail = await openInbox(api);

    const done = runAction(mail, { action: "read", ids: [row.id] });
    // At once, before the API answers.
    expect(mail.placeCounts.get(placeKey(1, "inbox"))?.unread).toBe(0);
    await done;

    expect(mail.placeCounts.get(placeKey(1, "inbox"))?.unread).toBe(0);
    expect(api.requests.filter((path) => path === "/api/v0/places")).toHaveLength(1);
  });

  it("keeps the answer's counts over a read of them in flight", async () => {
    const row = conversation(1, { unread: true });
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    api.places = [inbox];
    api.placesAfter = [{ ...inbox, unread: 0 }];
    const mail = await openInbox(api);
    // The read gives the count before the action, and answers once let go.
    let answerRead: (() => void) | undefined;
    const fetch = api.fetch;
    api.fetch = async (input, init) => {
      const url = input instanceof Request ? input.url : String(input);
      if (!url.endsWith("/places")) return fetch(input, init);
      const before = Response.json({ data: { places: [inbox] } });
      return new Promise((done) => {
        answerRead = () => done(before);
      });
    };
    vi.stubGlobal("fetch", vi.fn(api.fetch));
    const read = mail.client.refetchQueries({ queryKey: getListPlacesQueryKey() });

    await runAction(mail, { action: "read", ids: [row.id] });
    answerRead?.();
    await read;

    expect(mail.placeCounts.get(placeKey(1, "inbox"))?.unread).toBe(0);
  });

  it("reads the counts again when the answer has none", async () => {
    const row = conversation(1, { unread: true });
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    api.places = [inbox];
    const mail = await openInbox(api);
    // The server's count once the action is written.
    api.places = [{ ...inbox, unread: 0 }];

    await runAction(mail, { action: "read", ids: [row.id] });

    expect(mail.placeCounts.get(placeKey(1, "inbox"))?.unread).toBe(0);
    expect(api.requests.filter((path) => path === "/api/v0/places")).toHaveLength(2);
  });

  it("reads the counts again once actions answered out of order have all settled", async () => {
    const rows = [conversation(2, { unread: true }), conversation(1, { unread: true })];
    const api = mailApi(rows, [listedAccount(1, "ana@example.com")]);
    api.places = [{ ...inbox, unread: 2 }];
    // Each answer's counts as the server has them once it has applied it.
    Object.defineProperty(api, "placesAfter", {
      get: () => [{ ...inbox, unread: api.conversations.filter((c) => c.unread).length }],
    });
    const mail = await openInbox(api);
    // Each action is applied at once, and its answer waits to be let go.
    const answers: (() => void)[] = [];
    const fetch = api.fetch;
    api.fetch = async (input, init) => {
      const url = input instanceof Request ? input.url : String(input);
      if (!url.includes("/conversations/actions")) return fetch(input, init);
      const answer = await fetch(input, init);
      return new Promise((done) => answers.push(() => done(answer)));
    };
    vi.stubGlobal("fetch", vi.fn(api.fetch));

    const first = runAction(mail, { action: "read", ids: [rows[0].id] });
    await vi.waitFor(() => expect(answers).toHaveLength(1));
    const second = runAction(mail, { action: "read", ids: [rows[1].id] });
    await vi.waitFor(() => expect(answers).toHaveLength(2));
    // The newer answer first, then the older, which counts one unread.
    answers[1]();
    await second;
    answers[0]();
    await first;

    expect(mail.placeCounts.get(placeKey(1, "inbox"))?.unread).toBe(0);
  });

  it("puts a conversation back where it was by the answer's undo, though Move to Inbox would not", async () => {
    // Archived, so not in the Inbox list: an All mail list holds it. A
    // label's id is a UUID, as a change names it (ChangePlace).
    const work = "00000000-0000-4000-8000-0000000000a1";
    const row = conversation(1, { inbox: false, labelIds: [work] });
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    const mail = await openInbox(api);
    await mail.conversations.utils.writeUpsert(toConversation(row));

    const deleted = await runAction(mail, { action: "trash", ids: [row.id] });
    expect(mail.conversations.get(row.id)?.trash).toBe(true);
    const undone = runAction(mail, { changes: deleted.undo });
    // At once, before the API answers.
    expect(mail.conversations.get(row.id)).toMatchObject({
      trash: false,
      inbox: false,
      labelIds: [work],
    });
    await undone;

    expect(api.actions.at(-1)).toEqual({ action: "changes", ids: [row.id] });
    expect(mail.conversations.get(row.id)).toMatchObject({
      trash: false,
      inbox: false,
      allMail: true,
      labelIds: [work],
    });
  });

  it("stores nothing of an action answered after the session ended, and sends none still waiting", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    const label: PlaceCount = {
      accountId: 1,
      name: "Private",
      parent: null,
      place: "label-1",
      unread: 3,
    };
    api.places = [inbox];
    api.placesAfter = [inbox, label];
    api.refuse = { [row.id]: "needs-reconnect" };
    const mail = await openInbox(api);
    // The first action's answer waits to be let go.
    let answerFirst: (() => void) | undefined;
    const fetch = api.fetch;
    api.fetch = async (input, init) => {
      const url = input instanceof Request ? input.url : String(input);
      if (!url.includes("/conversations/actions") || answerFirst) return fetch(input, init);
      const answer = await fetch(input, init);
      return new Promise((done) => {
        answerFirst = () => done(answer);
      });
    };
    vi.stubGlobal("fetch", vi.fn(api.fetch));
    const first = runAction(mail, { action: "star", ids: [row.id] });
    await vi.waitFor(() => expect(answerFirst).toBeDefined());
    // Waits for the first's answer.
    const second = runAction(mail, { action: "unstar", ids: [row.id] });

    // When the member signs out and the next signs in on the same client.
    clearAccountCache(mail.client);
    const next: PlaceCount = { ...inbox, accountId: 2, unread: 5 };
    api.places = [next];
    const { placeCounts } = mailData(mail.client);
    placeCounts.subscribeChanges(() => {});
    await placeCounts.preload();
    answerFirst?.();
    await Promise.allSettled([first, second]);

    expect(mail.client.getQueryData(getListPlacesQueryKey())).toEqual({
      data: { places: [next] },
    });
    expect([...placeCounts.keys()]).toEqual([placeKey(2, "inbox")]);
    expect(api.actions).toEqual([{ action: "star", ids: [row.id] }]);
  });
});
