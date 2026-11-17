import { QueryClient } from "@tanstack/react-query";
import { createLiveQueryCollection } from "@tanstack/react-db";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { cleanup, render } from "vitest-browser-react";
import { closeToasts, Toaster } from "@/components/ui/toast";
import { clearAccountCache } from "@/features/auth/session-cache";
import { conversation, listedAccount, mailApi, type MailApi } from "@/test/mail-api";
import type { ActionMail } from "./actions";
import { mailData, toConversation, type Conversation } from "./conversations";
import { perform } from "./perform";
import { placeWhere } from "./rules";
import { UndoBar } from "./undo";
import { labelWords, moveWords } from "./words";

let current: QueryClient | undefined;

afterEach(async () => {
  closeToasts();
  await cleanup();
  if (current) clearAccountCache(current);
  current = undefined;
});

// Labels' ids, as List Places gives them.
const work = "00000000-0000-4000-8000-0000000000a1";
const travel = "00000000-0000-4000-8000-0000000000a2";

// openInbox serves api, holding the answer to the held-th request to
// Change Conversations (counting from 0) until released, and returns the
// mail of a client with an open Inbox list, as a mail page has it, beside
// the app's toasts.
async function openInbox(api: MailApi, held: number) {
  let changes = 0;
  const gate: { open?: () => void } = {};
  const waiting = new Promise<void>((done) => {
    gate.open = done;
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = input instanceof Request ? input.url : String(input);
      if (new URL(url, location.href).pathname === "/api/v0/conversations/changes") {
        if (changes++ === held) await waiting;
      }
      return api.fetch(input, init);
    }),
  );
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
  await render(<Toaster />);
  const mail: ActionMail = { client, conversations, placeCounts };
  return { mail, release: () => gate.open?.(), sent: () => changes };
}

// remove runs taking conversations out of labels, named names, as the
// picking bar's Labels does.
function remove(
  mail: ActionMail,
  conversations: Conversation[],
  labels: string[],
  names: string[],
) {
  return perform({
    mail,
    conversations,
    act: {
      changes: [{ ids: conversations.map((c) => c.id), remove: labels }],
      words: labelWords([], names),
    },
    from: "picked",
  });
}

// holdLater holds api's answer to a request to path, the next one after
// skip others, until release lets it go, then gives outcome: the answer,
// or a failure; reached reports whether that request is held.
function holdLater(
  api: MailApi,
  path: string,
  outcome: "answer" | "fail",
  skip = 0,
): { release: () => void; reached: () => boolean } {
  const gate: { open?: () => void } = {};
  const waiting = new Promise<void>((done) => {
    gate.open = done;
  });
  let held = false;
  let seen = 0;
  const fetch = vi.mocked(globalThis.fetch).getMockImplementation();
  if (!fetch) throw new Error("fetch is not served.");
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = input instanceof Request ? input.url : String(input);
      if (!held && new URL(url, location.href).pathname === path && seen++ === skip) {
        held = true;
        await waiting;
        if (outcome === "fail") api.failAction = 500;
      }
      return fetch(input, init);
    }),
  );
  return { release: () => gate.open?.(), reached: () => held };
}

// moveTo returns the move of the conversation id from the Inbox to Work,
// as Move to sends it.
function moveTo(id: string) {
  return {
    changes: [{ ids: [id], add: [work], remove: ["inbox" as const] }],
    words: moveWords("Work"),
  };
}

describe("perform", () => {
  it("says nothing of a slow Archive's failure once a later Move to moved the conversation", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    const { mail } = await openInbox(api, -1);
    const { release } = holdLater(api, "/api/v0/conversations/actions", "fail");
    const shown = toConversation(row);

    const archived = perform({ mail, conversations: [shown], act: "archive" });
    const moved = perform({ mail, conversations: [shown], act: moveTo(row.id) });
    release();

    expect((await archived).get(row.id)).toBe("failed");
    expect((await moved).get(row.id)).toBe("applied");
    expect(page.getByText("Couldn't archive the conversation").query()).toBeNull();
  });

  it("still says a slow Archive's failure after a later label left the Inbox as it was", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    const { mail } = await openInbox(api, -1);
    const { release } = holdLater(api, "/api/v0/conversations/actions", "fail");
    const shown = toConversation(row);

    const archived = perform({ mail, conversations: [shown], act: "archive" });
    const labelled = perform({
      mail,
      conversations: [shown],
      act: { changes: [{ ids: [row.id], add: [work] }], words: labelWords(["Work"], []) },
    });
    release();

    expect((await archived).get(row.id)).toBe("failed");
    expect((await labelled).get(row.id)).toBe("applied");
    await expect.element(page.getByText("Couldn't archive the conversation")).toBeVisible();
  });

  it("says nothing of a slow Move to's failure once a later Delete moved the conversation", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    const { mail } = await openInbox(api, -1);
    const { release } = holdLater(api, "/api/v0/conversations/changes", "fail");
    // Delete's answer waits too, so the Move to's failure lands alone.
    const { release: answerDelete } = holdLater(api, "/api/v0/conversations/actions", "answer");
    const shown = toConversation(row);

    const moved = perform({ mail, conversations: [shown], act: moveTo(row.id) });
    const deleted = perform({ mail, conversations: [shown], act: "trash" });
    release();

    expect((await moved).get(row.id)).toBe("failed");
    expect(page.getByText("Couldn't move the conversation to Work").query()).toBeNull();
    answerDelete();
    expect((await deleted).get(row.id)).toBe("applied");
  });

  it("keeps a newer failure shown when an older batch's late success covers it", async () => {
    // 26: two requests, the second held.
    const rows = Array.from({ length: 26 }, (_, n) => conversation(n + 1));
    const api = mailApi(rows, [listedAccount(1, "ana@example.com")]);
    const { mail } = await openInbox(api, -1);
    const { release, reached } = holdLater(api, "/api/v0/conversations/actions", "answer", 1);
    const shown = rows.map(toConversation);

    const deleted = perform({ mail, conversations: shown, act: "trash", from: "picked" });
    // The first request answered: its conversations are free to change.
    await expect.poll(reached).toBe(true);
    api.failAction = 500;
    await perform({
      mail,
      conversations: [shown[0]],
      act: { changes: [{ ids: [rows[0].id], add: [work] }], words: labelWords(["Work"], []) },
    });
    await expect
      .element(page.getByText("Couldn't change labels on the conversation"))
      .toBeVisible();
    release();

    expect((await deleted).get(rows[0].id)).toBe("applied");
    await expect
      .element(page.getByText("Couldn't change labels on the conversation"))
      .toBeVisible();
  });

  it("says nothing of an Undo's late failure once a later action put it right", async () => {
    // 26: two requests each way.
    const rows = Array.from({ length: 26 }, (_, n) => conversation(n + 1));
    const api = mailApi(rows, [listedAccount(1, "ana@example.com")]);
    const { mail } = await openInbox(api, -1);
    await render(<UndoBar fallbackFocus={{ current: null }} />);
    const shown = rows.map(toConversation);
    await perform({ mail, conversations: shown, act: "archive", from: "picked" });
    // The Undo's first request fails, and its second waits.
    api.failAction = 500;
    const { release, reached } = holdLater(api, "/api/v0/conversations/changes", "answer", 1);

    await userEvent.click(page.getByRole("button", { name: "Undo" }));
    await expect.poll(reached).toBe(true);
    // The member puts the first 25 back by hand.
    const first = Array.from({ length: 25 }, (_, n) =>
      toConversation(conversation(n + 1, { inbox: false })),
    );
    expect(
      (await perform({ mail, conversations: first, act: "unarchive", from: "picked" })).get(
        rows[0].id,
      ),
    ).toBe("applied");
    release();

    await expect.poll(() => api.changes.length).toBe(2);
    await new Promise((done) => setTimeout(done, 200));
    expect(page.getByText("Couldn't undo").query()).toBeNull();
  });

  it("says a slow batch's failure only of what no later action changed again", async () => {
    // 26 conversations: two requests, the second held.
    const api = mailApi(
      Array.from({ length: 26 }, (_, n) => conversation(n + 1, { labelIds: [work, travel] })),
      [listedAccount(1, "ana@example.com")],
    );
    const { mail, release, sent } = await openInbox(api, 1);
    const all = [...mail.conversations.values()];
    // In the first request, which answers at once: refused.
    const [first] = all;
    api.refuse[first.id] = "failed";

    const slow = remove(mail, all, [work, travel], ["Work", "Travel"]);
    await expect.poll(sent).toBe(2);
    delete api.refuse[first.id];
    // Taken out of Work again while the second request waits: that works.
    await remove(mail, [first], [work], ["Work"]);
    release();
    await slow;
    const error = page.getByText("Couldn't change labels on 1 conversation");
    await expect.element(error).toBeVisible();

    // Travel, the one change still failed, now works too.
    await remove(mail, [first], [travel], ["Travel"]);

    await expect.element(error).not.toBeInTheDocument();
  });
});
