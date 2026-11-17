import type { QueryClient } from "@tanstack/react-query";
import { createLiveQueryCollection } from "@tanstack/react-db";
import { afterEach, describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import type { ConversationMessage, ConversationRow, PlaceCount } from "@/api/generated/model";
import { conversation, listedAccount, mailApi, message, type MailApi } from "@/test/mail-api";
import { testQueryClient } from "@/test/query-client";
import { data, held, problem } from "@/test/server";
import { stubRouter } from "@/test/stub-router";
import { runAction } from "./actions";
import { mailData, placeKey, refreshNewest } from "./conversations";
import { Reader } from "./reader";
import { placeWhere } from "./rules";

let current: QueryClient | undefined;

afterEach(() => {
  // The tab's own visibility again, if a test hid it.
  Reflect.deleteProperty(document, "visibilityState");
});

// app renders the reading pane for id on api, opened from an open Inbox
// list holding its row or from a link.
async function app(api: MailApi, id: string, opened: "from-list" | "from-link" = "from-link") {
  api.server.install();
  const client = testQueryClient();
  current = client;
  // The mail page's collections, which its list and sidebar start.
  const { placeCounts } = mailData(client);
  placeCounts.subscribeChanges(() => {});
  await placeCounts.preload();
  if (opened === "from-list") {
    const list = createLiveQueryCollection((q) =>
      q
        .from({ c: mailData(client).conversations })
        .where(({ c }) => placeWhere(c, "inbox", undefined))
        .orderBy(({ c }) => c.dateMs, "desc")
        .limit(50),
    );
    // Kept open, as the list beside the pane is.
    list.subscribeChanges(() => {});
    await list.preload();
  }
  await stubRouter(
    <Reader
      conversationId={id}
      list={{
        places: ["inbox"],
        account: undefined,
        key: "inbox ",
        back: { to: "/mail/$place", params: { place: "inbox" } },
      }}
    />,
    { client },
  );
}

// answer makes api answer the read of conversation id by detail, a
// function of the read's number from 1, and everything else as before.
function answer(api: MailApi, id: string, detail: (read: number) => Response) {
  let reads = 0;
  api.server.on("GET", "/api/v0/conversations/{id}", ({ params }, earlier) =>
    params.id === id ? detail(++reads) : earlier(),
  );
}

// onlyMessage returns the one message of row, unread as row is, as
// Get Conversation reads it.
function onlyMessage(row: ConversationRow): ConversationMessage {
  return message(row, { unread: row.unread });
}

// withMessage makes api answer the read of row with its message, and
// returns how many times it was read so far.
function withMessage(api: MailApi, row: ConversationRow): () => number {
  let reads = 0;
  answer(api, row.id, (read) => {
    reads = read;
    return data({ conversation: row, messages: [onlyMessage(row)], providerUrl: null });
  });
  return () => reads;
}

// withProviderUrl makes api answer the read of row with its message and
// providerUrl, its address in Gmail.
function withProviderUrl(api: MailApi, row: ConversationRow, providerUrl: string): void {
  answer(api, row.id, () => data({ conversation: row, messages: [onlyMessage(row)], providerUrl }));
}

describe("Reader", () => {
  it("is Page not found for a listed conversation that no longer exists", async () => {
    const gone = conversation(1);
    const api = mailApi([gone], [listedAccount(1, "ana@example.com")]);
    answer(api, gone.id, () => problem(404, "/api/v0/problems/conversation-not-found"));
    await app(api, gone.id, "from-list");
    await expect.element(page.getByRole("heading", { name: "Page not found" })).toBeVisible();
  });

  it("shows the conversation in the view, its messages once read", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    api.messages[row.id] = [message(row)];
    await app(api, row.id);

    await expect.element(page.getByRole("heading", { level: 2, name: row.subject })).toHaveFocus();
    // The address beside the name, so a name alone can't pass for someone
    // else (DESIGN.md "Reading pane").
    await expect.element(page.getByText("<bo@example.com>")).toBeVisible();
    await expect.element(page.getByText("to me")).toBeVisible();
    await expect.element(page.getByText("1 message", { exact: true })).toBeVisible();

    // The reply editor gets the message and the member's accounts: it
    // answers from the receiving account, to the sender.
    await userEvent.click(page.getByRole("button", { name: "Reply", exact: true }).last());
    const editor = page.getByRole("form", { name: "Reply" });
    await expect
      .element(editor.getByRole("button", { name: /From/ }))
      .toHaveTextContent("ana@example.com");
    await expect
      .element(editor.getByRole("grid", { name: "To" }).getByRole("row"))
      .toHaveTextContent("bo@example.com");
  });

  it("draws the subject from the list's row while the messages load", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    // The read never answers.
    const never = held();
    api.server.on("GET", "/api/v0/conversations/{id}", async ({ params }, earlier) => {
      if (params.id === row.id) await never.promise;
      return earlier();
    });
    await app(api, row.id, "from-list");

    await expect.element(page.getByRole("heading", { level: 2, name: row.subject })).toBeVisible();
    await expect.element(page.getByText("Loading messages...")).toBeVisible();
  });

  it("keeps the subject, and says why with Try again, when the messages can't be read", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    answer(api, row.id, () => problem(500, "/api/v0/problems/internal-error"));
    await app(api, row.id, "from-list");

    await expect.element(page.getByRole("heading", { level: 2, name: row.subject })).toBeVisible();
    await expect
      .element(page.getByText("Couldn't load this conversation's messages."))
      .toBeVisible();
    await expect.element(page.getByRole("button", { name: "Try again" })).toBeVisible();
  });

  it("keeps a conversation shown when a later read finds it gone", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    answer(api, row.id, (read) =>
      read === 1
        ? data({ conversation: row, messages: [message(row)], providerUrl: null })
        : problem(404, "/api/v0/problems/conversation-not-found"),
    );
    await app(api, row.id);
    await expect.element(page.getByText("<bo@example.com>")).toBeVisible();

    await current?.refetchQueries();
    // The view has seen the failed read.
    await expect
      .poll(() =>
        current
          ?.getQueryCache()
          .getAll()
          .some((query) => query.state.status === "error"),
      )
      .toBe(true);
    await new Promise((done) => setTimeout(done, 100));

    // A reply being typed stays; Page not found waits for a fresh open.
    await expect.element(page.getByText("<bo@example.com>")).toBeVisible();
    expect(page.getByRole("heading", { name: "Page not found" }).elements()).toHaveLength(0);
  });

  it("says when the accounts can't be read, with Try again", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    api.failAccounts = 500;
    await app(api, row.id, "from-list");

    await expect.element(page.getByText("Couldn't load your accounts.")).toBeVisible();
    await expect.element(page.getByRole("button", { name: "Try again" })).toBeVisible();
  });
});

describe("Reader, opening", () => {
  const inbox: PlaceCount = { accountId: 1, name: null, parent: null, place: "inbox", unread: 1 };

  it("marks an unread conversation read once its messages are read, with its count", async () => {
    const row = conversation(1, { unread: true });
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    api.places = [inbox];
    api.placesAfter = [{ ...inbox, unread: 0 }];
    const reads = withMessage(api, row);
    await app(api, row.id, "from-list");

    await expect.poll(() => api.actions).toEqual([{ action: "read", ids: [row.id] }]);
    const mail = mailData(current!);
    expect(mail.conversations.get(row.id)?.unread).toBe(false);
    await expect.poll(() => mail.placeCounts.get(placeKey(1, "inbox"))?.unread).toBe(0);
    // Read again once the action settled, for the messages' own flags.
    await expect.poll(reads).toBe(2);
  });

  it("leaves a read conversation as it is", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    withMessage(api, row);
    await app(api, row.id, "from-list");

    await expect.element(page.getByText("<bo@example.com>")).toBeVisible();
    await new Promise((done) => setTimeout(done, 100));
    expect(api.actions).toEqual([]);
  });

  it("waits for the tab to show", async () => {
    let visibility: DocumentVisibilityState = "hidden";
    Object.defineProperty(document, "visibilityState", {
      configurable: true,
      get: () => visibility,
    });
    const row = conversation(1, { unread: true });
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    withMessage(api, row);
    await app(api, row.id, "from-list");
    await expect.element(page.getByText("<bo@example.com>")).toBeVisible();
    await new Promise((done) => setTimeout(done, 100));
    expect(api.actions).toEqual([]);

    visibility = "visible";
    document.dispatchEvent(new Event("visibilitychange"));

    await expect.poll(() => api.actions).toEqual([{ action: "read", ids: [row.id] }]);
  });

  it("leaves an account that takes no actions as it is, offering none", async () => {
    const row = conversation(1, { unread: true });
    // Usable, as when no Google client is set up: only allowedActions says.
    const account = {
      ...listedAccount(1, "ana@example.com"),
      allowedActions: ["rename" as const, "disconnect" as const],
    };
    const api = mailApi([row], [account]);
    withMessage(api, row);
    await app(api, row.id, "from-list");

    await expect.element(page.getByText("<bo@example.com>")).toBeVisible();
    const toolbar = page.getByRole("toolbar", { name: "Conversation actions" });
    expect(toolbar.getByRole("button", { name: "Archive" }).query()).toBeNull();
    expect(toolbar.getByRole("button", { name: "Star" }).query()).toBeNull();
    // No address in Gmail either: More would open empty.
    expect(toolbar.getByRole("button", { name: "More actions" }).query()).toBeNull();
    // The Inbox chip still opens the Inbox, with no × to archive.
    const marks = page.getByRole("grid", { name: "Labels" });
    await expect.element(marks.getByRole("row", { name: "Inbox" })).toBeVisible();
    expect(marks.getByRole("button").query()).toBeNull();
    await new Promise((done) => setTimeout(done, 100));
    expect(api.actions).toEqual([]);
  });

  it("offers More with Open in Gmail alone on an account that takes no actions", async () => {
    const row = conversation(1);
    const account = {
      ...listedAccount(1, "ana@example.com"),
      allowedActions: ["rename" as const, "disconnect" as const],
    };
    const api = mailApi([row], [account]);
    withProviderUrl(api, row, "https://mail.google.com/mail/#all/18c2");
    await app(api, row.id);

    await userEvent.click(page.getByRole("button", { name: "More actions" }));

    await expect.element(page.getByRole("menuitem", { name: /Open in Gmail/ })).toBeVisible();
    expect(page.getByRole("menuitem").elements()).toHaveLength(1);
  });

  it("leaves an account that needs reconnecting as it is", async () => {
    const row = conversation(1, { unread: true });
    const account = {
      ...listedAccount(1, "ana@example.com"),
      status: "needs-reconnect" as const,
      allowedActions: ["reconnect" as const, "rename" as const, "disconnect" as const],
    };
    const api = mailApi([row], [account]);
    withMessage(api, row);
    await app(api, row.id, "from-list");

    await expect.element(page.getByText("<bo@example.com>")).toBeVisible();
    await new Promise((done) => setTimeout(done, 100));
    expect(api.actions).toEqual([]);
  });

  it("puts a refused read back", async () => {
    const row = conversation(1, { unread: true });
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    api.places = [inbox];
    api.refuse[row.id] = "failed";
    withMessage(api, row);
    await app(api, row.id, "from-list");

    await expect.poll(() => api.actions).toEqual([{ action: "read", ids: [row.id] }]);
    const mail = mailData(current!);
    await expect.poll(() => mail.conversations.get(row.id)?.unread).toBe(true);
    expect(mail.placeCounts.get(placeKey(1, "inbox"))?.unread).toBe(1);
  });

  it("keeps its row while open, after its list no longer holds it", async () => {
    // Not the page's oldest, whose date's other conversations the list
    // also loads apart, which would hold it too.
    const row = conversation(2);
    const api = mailApi([conversation(1), row], [listedAccount(1, "ana@example.com")]);
    withMessage(api, row);
    await app(api, row.id, "from-list");
    await expect.element(page.getByText("<bo@example.com>")).toBeVisible();

    // Archived elsewhere: the Inbox's next read leaves it out.
    row.inbox = false;
    await refreshNewest(current!);

    expect(mailData(current!).conversations.has(row.id)).toBe(true);
    await expect.element(page.getByRole("heading", { level: 2, name: row.subject })).toBeVisible();
  });

  it("loads a conversation no list holds into the mail, with the one read", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    const reads = withMessage(api, row);
    await app(api, row.id);

    await expect.element(page.getByText("<bo@example.com>")).toBeVisible();
    await expect
      .poll(() => mailData(current!).conversations.get(row.id)?.subject)
      .toBe(row.subject);
    expect(reads()).toBe(1);
  });

  it("keeps a conversation no list holds through a failed refresh, and its toolbar", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    answer(api, row.id, (read) =>
      read === 1
        ? data({ conversation: row, messages: [onlyMessage(row)], providerUrl: null })
        : problem(503, "/api/v0/problems/service-busy"),
    );
    await app(api, row.id);
    const toolbar = page.getByRole("toolbar", { name: "Conversation actions" });
    await expect.element(toolbar).toBeVisible();

    await refreshNewest(current!);

    expect(mailData(current!).conversations.has(row.id)).toBe(true);
    await expect.element(toolbar).toBeVisible();
  });

  it("puts a conversation no list holds back as it was when an action fails across a failed refresh", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    answer(api, row.id, (read) =>
      read === 1
        ? data({ conversation: row, messages: [onlyMessage(row)], providerUrl: null })
        : problem(503, "/api/v0/problems/service-busy"),
    );
    // The action's answer, a failure, waits to be let go.
    const action = held();
    api.server.on("POST", "/api/v0/conversations/actions", async () => {
      await action.promise;
      return problem(500, "/api/v0/problems/internal-error");
    });
    await app(api, row.id);
    await expect.element(page.getByRole("toolbar", { name: "Conversation actions" })).toBeVisible();
    const mail = mailData(current!);
    const starring = runAction(
      { client: current!, conversations: mail.conversations, placeCounts: mail.placeCounts },
      { action: "star", ids: [row.id] },
    );
    await expect.poll(() => api.requests).toContain("/api/v0/conversations/actions");
    expect(mail.conversations.get(row.id)?.starred).toBe(true);

    await refreshNewest(current!);
    action.resolve();
    await expect(starring).rejects.toMatchObject({ status: 500 });

    expect(mail.conversations.get(row.id)?.starred).toBe(false);
  });

  it("lets a conversation no list holds go once a refresh finds it gone", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    answer(api, row.id, (read) =>
      read === 1
        ? data({ conversation: row, messages: [onlyMessage(row)], providerUrl: null })
        : problem(404, "/api/v0/problems/not-found"),
    );
    await app(api, row.id);
    await expect.element(page.getByText("<bo@example.com>")).toBeVisible();

    await refreshNewest(current!);

    await expect.poll(() => mailData(current!).conversations.has(row.id)).toBe(false);
  });

  it("opens the conversation in Gmail in a new tab, with no way back to inbok", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    const url = `https://mail.google.com/mail/?authuser=ana%40example.com#all/${row.id}`;
    withProviderUrl(api, row, url);
    await app(api, row.id);

    await userEvent.click(page.getByRole("button", { name: "More actions" }));

    const link = page.getByRole("menuitem", { name: /Open in Gmail/ });
    await expect.element(link).toHaveAttribute("href", url);
    await expect.element(link).toHaveAttribute("target", "_blank");
    await expect.element(link).toHaveAttribute("rel", "noopener noreferrer");
  });

  it("offers no Open in Gmail for an address that is not https", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    withProviderUrl(api, row, "javascript:alert(1)");
    await app(api, row.id);
    await expect.element(page.getByText("<bo@example.com>")).toBeVisible();

    // More still holds Report spam.
    await userEvent.click(page.getByRole("button", { name: "More actions" }));

    await expect.element(page.getByRole("menuitem", { name: "Report spam" })).toBeVisible();
    expect(page.getByRole("menuitem", { name: /Open in Gmail/ }).query()).toBeNull();
  });
});
