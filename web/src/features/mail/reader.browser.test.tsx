import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  RouterProvider,
  createMemoryHistory,
  createRootRouteWithContext,
  createRouter,
} from "@tanstack/react-router";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { cleanup, render } from "vitest-browser-react";
import type { ConversationMessage } from "@/api/generated/model";
import { clearAccountCache } from "@/features/auth/session-cache";
import { Route as appRoot } from "@/routes/__root";
import { conversation, listedAccount, mailApi, type MailApi } from "@/test/mail-api";
import { mailData, toConversation } from "./conversations";
import { Reader } from "./reader";

let current: QueryClient | undefined;

afterEach(async () => {
  await cleanup();
  if (current) clearAccountCache(current);
  current = undefined;
  vi.unstubAllGlobals();
});

// app renders the reading pane for id on api, with row already in the
// list's collection when given, and returns its router.
async function app(api: MailApi, id: string, row?: ReturnType<typeof conversation>) {
  vi.stubGlobal("fetch", vi.fn(api.fetch));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  current = client;
  if (row) await mailData(client).conversations.utils.writeUpsert(toConversation(row));
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
  api.fetch = async (input) => {
    const url = input instanceof Request ? input.url : input.toString();
    if (url.endsWith(`/conversations/${id}`)) return detail(++reads);
    if (url.includes("/content"))
      return Response.json({ data: { remote: "none", attachments: [], bodies: [] } });
    return fetch(input);
  };
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
    await app(mailApi([], [listedAccount(1, "ana@example.com")]), gone.id, gone);
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
    await app(api, row.id, row);

    await expect.element(page.getByRole("heading", { level: 2, name: row.subject })).toBeVisible();
    await expect.element(page.getByText("Loading messages...")).toBeVisible();
  });

  it("keeps the subject, and says why with Try again, when the messages can't be read", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    answer(api, row.id, () => problem(500, "internal-error"));
    await app(api, row.id, row);

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
    const router = await app(api, row.id, row);

    await expect.poll(() => router.state.location.pathname).toBe("/sign-in");
  });

  it("says when the accounts can't be read, with Try again", async () => {
    const row = conversation(1);
    const api = mailApi([row], [listedAccount(1, "ana@example.com")]);
    api.failAccounts = 500;
    await app(api, row.id, row);

    await expect.element(page.getByText("Couldn't load your accounts.")).toBeVisible();
    await expect.element(page.getByRole("button", { name: "Try again" })).toBeVisible();
  });
});
