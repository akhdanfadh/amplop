import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createLiveQueryCollection } from "@tanstack/react-db";
import {
  RouterProvider,
  createMemoryHistory,
  createRootRouteWithContext,
  createRouter,
} from "@tanstack/react-router";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { cleanup, render } from "vitest-browser-react";
import type { ConversationMessage, ConversationRow } from "@/api/generated/model";
import { clearAccountCache } from "@/features/auth/session-cache";
import { Route as appRoot } from "@/routes/__root";
import { conversation, listedAccount, mailApi, type MailApi } from "@/test/mail-api";
import { runAction } from "./actions";
import { mailData, refreshNewest } from "./conversations";
import { Reader } from "./reader";
import { placeWhere } from "./rules";

let current: QueryClient | undefined;

afterEach(async () => {
  await cleanup();
  if (current) clearAccountCache(current);
  current = undefined;
  vi.unstubAllGlobals();
});

// app renders the reading pane for id on api, opened from an open Inbox
// list holding its row or from a link, and returns its router.
async function app(api: MailApi, id: string, opened: "from-list" | "from-link" = "from-link") {
  vi.stubGlobal("fetch", vi.fn(api.fetch));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
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
  const root = createRootRouteWithContext<{ queryClient: QueryClient }>()({
    component: () => (
      <Reader conversationId={id} back={{ to: "/mail/$place", params: { place: "inbox" } }} />
    ),
  });
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory(),
    context: { queryClient: client },
    Wrap: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
  });
  await render(<RouterProvider router={router} />);
  void appRoot;
  return router;
}

// answer makes api answer the read of conversation id by detail, a
// function of the read's number from 1, and everything else as before.
function answer(api: MailApi, id: string, detail: (read: number) => Response) {
  const fetch = api.fetch;
  let reads = 0;
  api.fetch = async (input, init) => {
    const url = input instanceof Request ? input.url : input.toString();
    if (url.endsWith(`/conversations/${id}`)) return detail(++reads);
    if (url.includes("/content"))
      return Response.json({ data: { remote: "none", attachments: [], bodies: [] } });
    return fetch(input, init);
  };
}

// onlyMessage returns the one message of row, as Get Conversation reads
// it.
function onlyMessage(row: ConversationRow): ConversationMessage {
  return {
    id: "00000000-0000-4000-8000-000000000901",
    date: row.date,
    subject: row.subject,
    preview: "Hello",
    from: [{ name: "Bo Tan", address: "bo@example.com" }],
    to: [{ name: "", address: "ana@example.com" }],
    cc: [],
    replyTo: [],
    labelIds: [],
    spam: false,
    trash: false,
    sent: false,
    starred: false,
    unread: row.unread,
    answers: null,
    attachments: false,
  };
}

// withMessage makes api answer the read of row with its message, and
// returns how many times it was read so far.
function withMessage(api: MailApi, row: ConversationRow): () => number {
  let reads = 0;
  answer(api, row.id, (read) => {
    reads = read;
    return Response.json({
      data: { conversation: row, messages: [onlyMessage(row)], providerUrl: null },
    });
  });
  return () => reads;
}

// problem returns a Problem Details response of status.
function problem(status: number, type: string): Response {
  return Response.json(
    { type: `/api/v0/problems/${type}`, status, title: "Refused", detail: "Refused." },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

describe("Reader", () => {
  it("is Page not found for a listed conversation that no longer exists", async () => {
    const gone = conversation(1);
    const api = mailApi([gone], [listedAccount(1, "ana@example.com")]);
    answer(api, gone.id, () => problem(404, "conversation-not-found"));
    await app(api, gone.id, "from-list");
    await expect.element(page.getByRole("heading", { name: "Page not found" })).toBeVisible();
  });

  it("shows the conversation in the view, its messages once read", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    const message: ConversationMessage = {
      id: "00000000-0000-4000-8000-000000000901",
      date: row.date,
      subject: row.subject,
      preview: "Hello",
      from: [{ name: "Bo Tan", address: "bo@example.com" }],
      to: [{ name: "", address: "ana@example.com" }],
      cc: [],
      replyTo: [],
      labelIds: [],
      spam: false,
      trash: false,
      sent: false,
      starred: false,
      unread: false,
      answers: null,
      attachments: false,
    };
    const detail = Response.json({
      data: { conversation: row, messages: [message], providerUrl: null },
    });
    const fetch = api.fetch;
    api.fetch = async (input) => {
      const url = input instanceof Request ? input.url : input.toString();
      if (url.endsWith(`/conversations/${row.id}`)) return detail.clone();
      // The message's content, which the view reads for an open message.
      if (url.includes(`/messages/${message.id}/content`))
        return Response.json({ data: { remote: "none", attachments: [], bodies: [] } });
      return fetch(input);
    };
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
    const fetch = api.fetch;
    // The read never answers.
    api.fetch = async (input) =>
      (input instanceof Request ? input.url : input.toString()).endsWith(`/conversations/${row.id}`)
        ? new Promise<Response>(() => {})
        : fetch(input);
    await app(api, row.id, "from-list");

    await expect.element(page.getByRole("heading", { level: 2, name: row.subject })).toBeVisible();
    await expect.element(page.getByText("Loading messages...")).toBeVisible();
  });

  it("keeps the subject, and says why with Try again, when the messages can't be read", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    answer(api, row.id, () => problem(500, "internal-error"));
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
    const message: ConversationMessage = {
      id: "00000000-0000-4000-8000-000000000901",
      date: row.date,
      subject: row.subject,
      preview: "Hello",
      from: [{ name: "Bo Tan", address: "bo@example.com" }],
      to: [{ name: "", address: "ana@example.com" }],
      cc: [],
      replyTo: [],
      labelIds: [],
      spam: false,
      trash: false,
      sent: false,
      starred: false,
      unread: false,
      answers: null,
      attachments: false,
    };
    answer(api, row.id, (read) =>
      read === 1
        ? Response.json({ data: { conversation: row, messages: [message], providerUrl: null } })
        : problem(404, "conversation-not-found"),
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

  it("opens sign-in when the read finds the session ended", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    answer(api, row.id, () => problem(401, "unauthenticated"));
    const router = await app(api, row.id, "from-list");

    await expect.poll(() => router.state.location.pathname).toBe("/sign-in");
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

  it("keeps a conversation no list holds through a failed refresh", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    answer(api, row.id, (read) =>
      read === 1
        ? Response.json({
            data: { conversation: row, messages: [onlyMessage(row)], providerUrl: null },
          })
        : problem(503, "service-busy"),
    );
    await app(api, row.id);
    await expect.poll(() => mailData(current!).conversations.has(row.id)).toBe(true);

    await refreshNewest(current!);

    expect(mailData(current!).conversations.has(row.id)).toBe(true);
    await expect.element(page.getByRole("heading", { level: 2, name: row.subject })).toBeVisible();
  });

  it("puts a conversation no list holds back as it was when an action fails across a failed refresh", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    answer(api, row.id, (read) =>
      read === 1
        ? Response.json({
            data: { conversation: row, messages: [onlyMessage(row)], providerUrl: null },
          })
        : problem(503, "service-busy"),
    );
    // The action's answer, a failure, waits to be let go.
    let answerAction: (() => void) | undefined;
    const fetch = api.fetch;
    api.fetch = async (input, init) => {
      const url = input instanceof Request ? input.url : String(input);
      if (!url.includes("/conversations/actions")) return fetch(input, init);
      await new Promise<void>((done) => {
        answerAction = done;
      });
      return problem(500, "internal");
    };
    await app(api, row.id);
    const mail = mailData(current!);
    await expect.poll(() => mail.conversations.has(row.id)).toBe(true);
    const starring = runAction(
      { client: current!, conversations: mail.conversations, placeCounts: mail.placeCounts },
      { action: "star", ids: [row.id] },
    );
    await vi.waitFor(() => expect(answerAction).toBeDefined());
    expect(mail.conversations.get(row.id)?.starred).toBe(true);

    await refreshNewest(current!);
    answerAction?.();
    await expect(starring).rejects.toMatchObject({ status: 500 });

    expect(mail.conversations.get(row.id)?.starred).toBe(false);
  });

  it("lets a conversation no list holds go once a refresh finds it gone", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    answer(api, row.id, (read) =>
      read === 1
        ? Response.json({
            data: { conversation: row, messages: [onlyMessage(row)], providerUrl: null },
          })
        : problem(404, "not-found"),
    );
    await app(api, row.id);
    await expect.element(page.getByText("<bo@example.com>")).toBeVisible();

    await refreshNewest(current!);

    await expect.poll(() => mailData(current!).conversations.has(row.id)).toBe(false);
  });
});
