import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router";
import { useState } from "react";
import { describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import type { ListedAccount, OutgoingMessage } from "@/api/generated/model";
import { shortcutModifier } from "@/lib/shortcut";
import { ConversationReply, ReplyEditorSlot, ReplyProvider } from "./reply";
import type { AnsweredMessage } from "./reply-defaults";
import { editNote } from "./text";

const ok = 200;
const created = 201;
const notFound = 404;

const studio: ListedAccount = {
  id: 7,
  provider: "gmail",
  email: "studio@example.com",
  name: null,
  color: 1,
  status: "usable",
  createdAt: "2026-11-01T00:00:00Z",
  needsReconnectSince: null,
  allowedActions: ["sync", "send", "reconnect", "rename", "disconnect"],
  sync: {
    status: "complete",
    failure: null,
    failedAt: null,
    lastSyncedAt: "2026-11-13T08:00:00Z",
    messagesSoFar: null,
  },
};
const personal: ListedAccount = { ...studio, id: 8, email: "me@example.org" };

// A message Ana sent to Studio and Ben.
const answered: AnsweredMessage = {
  id: "019a0000-0000-7000-8000-000000000001",
  accountId: studio.id,
  from: [{ name: "Ana", address: "ana@example.com" }],
  to: [
    { name: "Studio", address: "studio@example.com" },
    { name: "", address: "ben@example.com" },
  ],
  cc: [],
  replyTo: [],
  sent: false,
};

// saved returns the outgoing message the server saved for a reply, in status.
function saved(
  status: OutgoingMessage["status"],
  failure: OutgoingMessage["failure"] = null,
): OutgoingMessage {
  return {
    id: "019a0000-0000-7000-8000-0000000000aa",
    status,
    failure,
    from: studio.email,
    connectedAccountId: studio.id,
    to: ["ana@example.com"],
    cc: [],
    bcc: [],
    subject: "Re: Lunch",
    body: "Noon works.",
    createdAt: "2026-11-13T08:00:00Z",
    sentAt: status === "sent" ? "2026-11-13T08:00:05Z" : null,
    sentAgainAs: null,
    dismissedAt: null,
    outstanding: status !== "sent",
    reply: false,
    // As the API lists them: Send again needs an account that can send;
    // Dismiss needs nothing of it.
    allowedActions:
      status === "failed" || status === "unknown"
        ? failure === "needs-reconnect"
          ? ["dismiss"]
          : ["send-again", "dismiss"]
        : [],
  };
}

type Call = { method: string; url: string; headers: Headers; body: unknown };

// problem returns an API problem response with status and type.
function problem(status: number, type: string, extra: object = {}): Response {
  return Response.json(
    { type, status, title: "Problem", detail: "The server's detail.", ...extra },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

// serve stubs the API: the accounts read answers with accounts; Reply to
// Message, at any answered message, answers with reply, a response; each
// read of the Outbox, which gives the saved message's status, lists its
// next status in statuses, the last one repeating, or answers a 503 once
// failReadsFrom reads have passed; Send Message Again answers with again,
// by default the message as last listed; and a read of the message itself
// answers with the status the Outbox last listed. It returns the calls the
// page made.
function serve({
  accounts = [studio, personal],
  reply = () => Response.json({ data: saved("waiting") }, { status: created }),
  statuses = [saved("waiting")],
  failReadsFrom = Infinity,
  again,
}: {
  again?: () => Response;
  accounts?: ListedAccount[];
  reply?: () => Response;
  statuses?: OutgoingMessage[];
  failReadsFrom?: number;
} = {}): Call[] {
  const calls: Call[] = [];
  let reads = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn<(url: string, init?: RequestInit) => Promise<Response>>(async (url, init = {}) => {
      const method = init.method ?? "GET";
      const body = typeof init.body === "string" ? JSON.parse(init.body) : undefined;
      calls.push({ method, url, headers: new Headers(init.headers), body });
      if (url === "/api/v0/connected-accounts")
        return Response.json({ data: { accounts, syncStopped: false } }, { status: ok });
      if (method === "POST" && /^\/api\/v0\/messages\/[^/]+\/replies$/.test(url)) return reply();
      if (again && method === "POST" && url.endsWith("/send-again")) return again();
      if (url === "/api/v0/outgoing-messages") {
        const status = statuses[Math.min(reads, statuses.length - 1)];
        reads++;
        if (reads > failReadsFrom) return problem(503, "/api/v0/problems/service-busy");
        return Response.json({ data: { messages: [status] } }, { status: ok });
      }
      if (url.startsWith("/api/v0/outgoing-messages/")) {
        const status = statuses[Math.min(Math.max(reads - 1, 0), statuses.length - 1)];
        return Response.json({ data: status }, { status: ok });
      }
      return Response.json({}, { status: notFound });
    }),
  );
  return calls;
}

// newer is a message that arrives in the conversation after answered.
const newer: AnsweredMessage = {
  ...answered,
  id: "019a0000-0000-7000-8000-000000000002",
  from: [{ name: "Ben", address: "ben@example.com" }],
};

// Conversation renders the reply part of a conversation whose last message
// starts as message, with a button that makes newer the last message, as
// a sync would. Each message has the place below it where the editor
// answering it shows, as the conversation view gives it.
function Conversation({ message }: { message: AnsweredMessage }) {
  const [last, setLast] = useState(message);
  return (
    <>
      <h1>Lunch</h1>
      <ReplyProvider>
        <ReplyEditorSlot messageId={message.id} />
        <ReplyEditorSlot messageId={newer.id} />
        <ConversationReply message={last} />
      </ReplyProvider>
      <button type="button" onClick={() => setLast(newer)}>
        Newer message arrives
      </button>
    </>
  );
}

// show renders the reply part of a conversation whose last message is
// message, inside a router, as the conversation view places it. It
// returns the query client.
async function show(message: AnsweredMessage = answered) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const root = createRootRoute();
  const conversation = createRoute({
    getParentRoute: () => root,
    path: "/",
    component: () => (
      <QueryClientProvider client={client}>
        <Conversation message={message} />
      </QueryClientProvider>
    ),
  });
  const router = createRouter({
    routeTree: root.addChildren([conversation]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  await render(<RouterProvider router={router} />);
  return { client };
}

describe("ConversationReply", () => {
  it("offers Reply all only when someone else received the message", async () => {
    await page.viewport(1440, 900);
    serve();
    await show({ ...answered, to: [{ name: "", address: "studio@example.com" }] });

    await expect.element(page.getByRole("button", { name: "Reply", exact: true })).toBeVisible();
    await expect.element(page.getByRole("button", { name: "Reply all" })).not.toBeInTheDocument();
  });

  it("opens from the receiving account, to the sender, with focus in the message", async () => {
    await page.viewport(1440, 900);
    serve();
    await show();

    await userEvent.click(page.getByRole("button", { name: "Reply all" }));

    const editor = page.getByRole("form", { name: "Reply all" });
    await expect.element(editor).toBeVisible();
    await expect
      .element(editor.getByRole("button", { name: /From/ }))
      .toHaveTextContent("studio@example.com");
    await expect
      .element(editor.getByRole("grid", { name: "To" }).getByRole("row"))
      .toHaveTextContent("ana@example.com");
    await expect
      .element(editor.getByRole("grid", { name: "Cc" }).getByRole("row"))
      .toHaveTextContent("ben@example.com");
    // Bcc is empty, so it waits behind its button.
    await expect.element(editor.getByRole("textbox", { name: "Bcc" })).not.toBeInTheDocument();
    await expect.element(editor.getByRole("textbox", { name: "Message" })).toHaveFocus();
    await expect
      .element(page.getByRole("button", { name: "Reply", exact: true }))
      .not.toBeInTheDocument();
  });

  it("sends with Ctrl+Enter, with a key, and shows how the send goes", async () => {
    await page.viewport(1440, 900);
    const calls = serve({ statuses: [saved("sending"), saved("sent")] });
    await show();

    await userEvent.click(page.getByRole("button", { name: "Reply", exact: true }));
    await userEvent.keyboard("Noon works.{Control>}{Enter}{/Control}");

    await expect
      .element(page.getByRole("status"), { timeout: 5_000 })
      .toMatchTextContent(/^Sent to ana@example\.comFrom studio@example\.com/);
    const post = calls.find((call) => call.method === "POST");
    expect(post?.url).toBe(`/api/v0/messages/${answered.id}/replies`);
    expect(post?.headers.get("Idempotency-Key")).toMatch(/^"[0-9a-f-]{36}"$/);
    expect(post?.body).toEqual({
      connectedAccountId: studio.id,
      to: ["ana@example.com"],
      cc: [],
      bcc: [],
      body: "Noon works.",
    });
    await expect.element(page.getByRole("form")).not.toBeInTheDocument();
    await expect.element(page.getByRole("link", { name: "Open in Outbox" })).toBeVisible();
  });

  it("names the send shortcut in Send's tooltip, which describes the button", async () => {
    await page.viewport(1440, 900);
    serve();
    await show();

    await userEvent.click(page.getByRole("button", { name: "Reply", exact: true }));
    // From the message, Tab reaches Send; keyboard focus shows its tooltip at once.
    await userEvent.keyboard("{Tab}");

    const send = page.getByRole("button", { name: "Send", exact: true });
    await expect.element(send).toHaveFocus();
    // shortcut.test.ts covers which key each platform names.
    const tooltip = `Send (${shortcutModifier()}+Enter)`;
    await expect.element(page.getByRole("tooltip")).toHaveTextContent(tooltip);
    await expect.element(send).toHaveAccessibleDescription(tooltip);
  });

  it("keeps the typed text when the answered message was deleted", async () => {
    await page.viewport(1440, 900);
    serve({
      reply: () =>
        Response.json(
          {
            type: "/api/v0/problems/message-not-found",
            status: notFound,
            title: "Message not found",
            detail: "The message does not exist. It may have been deleted.",
          },
          { status: notFound, headers: { "Content-Type": "application/problem+json" } },
        ),
    });
    await show();

    await userEvent.click(page.getByRole("button", { name: "Reply", exact: true }));
    await userEvent.keyboard("Noon works.");
    await userEvent.click(page.getByRole("button", { name: "Send" }));

    await expect.element(page.getByRole("alert")).toMatchTextContent("was deleted");
    await expect.element(page.getByRole("textbox", { name: "Message" })).toHaveValue("Noon works.");
  });

  it("asks before discarding typed text, then returns focus to Reply", async () => {
    await page.viewport(1440, 900);
    serve();
    await show();

    await userEvent.click(page.getByRole("button", { name: "Reply", exact: true }));
    await userEvent.keyboard("Noon works.");
    await userEvent.click(page.getByRole("button", { name: "Discard" }));
    const dialog = page.getByRole("alertdialog", { name: "Discard this reply?" });
    await expect.element(dialog.getByRole("button", { name: "Keep editing" })).toHaveFocus();
    // The keyboard, since the dialog ignores a click this soon after it
    // opens, as a double click's second half.
    await userEvent.keyboard("{Tab}{Enter}");

    await expect.element(page.getByRole("form")).not.toBeInTheDocument();
    await expect.element(page.getByRole("button", { name: "Reply", exact: true })).toHaveFocus();
  });

  // Discard then drops only the editor: the reply may already be on its
  // way, which the member should know before the editor goes.
  it("asks before discarding a reply an unanswered send may have saved, even unedited", async () => {
    await page.viewport(1440, 900);
    serve({
      reply: () => Response.json({ data: saved("failed", "refused") }, { status: created }),
      statuses: [saved("failed", "refused")],
      again: () => problem(500, "/api/v0/problems/internal-error"),
    });
    await show();
    await userEvent.click(page.getByRole("button", { name: "Reply", exact: true }));
    await userEvent.keyboard("Noon works.");
    await userEvent.click(page.getByRole("button", { name: "Send" }));
    await userEvent.click(page.getByRole("button", { name: "Edit and send" }));

    // Nothing edited: the editor starts with the refused reply's content.
    const editor = page.getByRole("form", { name: "Reply" });
    await userEvent.click(editor.getByRole("button", { name: "Send" }));
    await expect.element(editor.getByRole("alert")).toBeVisible();
    await userEvent.click(editor.getByRole("button", { name: "Discard" }));

    await expect
      .element(page.getByRole("alertdialog", { name: "Discard this reply?" }))
      .toMatchTextContent("An earlier try may have saved this reply");
  });

  it("returns focus to the bottom's Reply all after Discard", async () => {
    serve();
    await show();

    await userEvent.click(page.getByRole("button", { name: "Reply all" }));
    await userEvent.click(page.getByRole("button", { name: "Discard" }));

    await expect.element(page.getByRole("button", { name: "Reply all" })).toHaveFocus();
  });

  it("discards an untouched editor at once", async () => {
    await page.viewport(1440, 900);
    serve();
    await show();

    await userEvent.click(page.getByRole("button", { name: "Reply", exact: true }));
    await userEvent.click(page.getByRole("button", { name: "Discard" }));

    await expect.element(page.getByRole("form")).not.toBeInTheDocument();
    await expect.element(page.getByRole("alertdialog")).not.toBeInTheDocument();
  });

  it("says when the reply goes from an account that didn't receive the message", async () => {
    await page.viewport(1440, 900);
    serve();
    await show();

    await userEvent.click(page.getByRole("button", { name: "Reply", exact: true }));
    const editor = page.getByRole("form", { name: "Reply" });
    await expect.element(editor).not.toMatchTextContent("new conversation");
    await userEvent.click(editor.getByRole("button", { name: /From/ }));
    await userEvent.click(page.getByRole("option", { name: "me@example.org" }));

    await expect
      .element(editor)
      .toMatchTextContent(
        "This account didn't receive the message, so the reply starts a new conversation in its Gmail.",
      );
  });

  it("names each From account by its address and the name the member chose", async () => {
    await page.viewport(1440, 900);
    serve({ accounts: [{ ...studio, name: "Studio" }, personal] });
    await show();

    await userEvent.click(page.getByRole("button", { name: "Reply", exact: true }));
    const editor = page.getByRole("form", { name: "Reply" });
    await expect
      .element(editor.getByRole("button", { name: /From/ }))
      .toHaveTextContent("studio@example.comStudio");
    await userEvent.click(editor.getByRole("button", { name: /From/ }));

    await expect
      .element(page.getByRole("option", { name: "studio@example.com Studio" }))
      .toBeVisible();
    await expect.element(page.getByRole("option", { name: "me@example.org" })).toBeVisible();
    // The dot sits on the middle of the address, in the list and in From.
    for (const box of [
      page.getByRole("option", { name: "me@example.org" }).element(),
      editor.getByRole("button", { name: /From/ }).element(),
    ]) {
      const dot = box.querySelector("[aria-hidden='true'].rounded-full")?.getBoundingClientRect();
      const address = [...box.querySelectorAll("[translate='no']")][0]?.getBoundingClientRect();
      expect(
        Math.abs(
          (dot?.top ?? 0) +
            (dot?.height ?? 0) / 2 -
            ((address?.top ?? 0) + (address?.height ?? 0) / 2),
        ),
      ).toBeLessThanOrEqual(1);
    }
  });

  it("adds a Bcc row on demand, with focus in it", async () => {
    await page.viewport(1440, 900);
    serve();
    await show();

    await userEvent.click(page.getByRole("button", { name: "Reply", exact: true }));
    await userEvent.click(page.getByRole("button", { name: "Add Bcc recipients" }));

    await expect.element(page.getByRole("textbox", { name: "Bcc" })).toHaveFocus();
    await expect
      .element(page.getByRole("button", { name: "Add Bcc recipients" }))
      .not.toBeInTheDocument();
  });

  it("asks for an account when the receiving one needs reconnecting", async () => {
    await page.viewport(1440, 900);
    serve({
      accounts: [
        {
          ...studio,
          status: "needs-reconnect",
          allowedActions: ["reconnect", "rename", "disconnect"],
        },
        personal,
      ],
    });
    await show();

    await userEvent.click(page.getByRole("button", { name: "Reply", exact: true }));

    const editor = page.getByRole("form", { name: "Reply" });
    await expect
      .element(editor.getByRole("button", { name: /From/ }))
      .toHaveTextContent("Choose an account");
    await expect
      .element(editor)
      .toMatchTextContent("studio@example.com needs reconnecting before it can send.");
  });

  it("says the accounts can't send yet when the receiving one is usable but can't", async () => {
    await page.viewport(1440, 900);
    // As when no Google client is set up: usable, yet without send.
    const cannotSend = { ...studio, allowedActions: ["rename" as const, "disconnect" as const] };
    serve({ accounts: [cannotSend, { ...personal, allowedActions: cannotSend.allowedActions }] });
    await show();

    await userEvent.click(page.getByRole("button", { name: "Reply", exact: true }));

    const editor = page.getByRole("form", { name: "Reply" });
    await expect
      .element(editor.getByRole("button", { name: /From/ }))
      .toHaveTextContent("Choose an account");
    await expect
      .element(editor)
      .toMatchTextContent(
        "Your Gmail accounts can't send yet. The Connected accounts page says why.",
      );
  });

  it("sends a refused reply again with the member's edits, from its own account", async () => {
    await page.viewport(1440, 900);
    const calls = serve({
      reply: () => Response.json({ data: saved("failed", "refused") }, { status: created }),
      statuses: [saved("failed", "refused")],
    });
    await show();

    await userEvent.click(page.getByRole("button", { name: "Reply", exact: true }));
    await userEvent.keyboard("Noon works.");
    await userEvent.click(page.getByRole("button", { name: "Send" }));
    // The reply's card offers what the Outbox does, Dismiss too.
    await expect
      .element(
        page.getByRole("region", { name: "Status" }).getByRole("button", { name: "Dismiss" }),
      )
      .toBeVisible();
    await userEvent.click(page.getByRole("button", { name: "Edit and send" }));

    const editor = page.getByRole("form", { name: "Reply" });
    await expect.element(editor).toMatchTextContent(editNote);
    await expect
      .element(editor.getByRole("textbox", { name: "Message" }))
      .toHaveValue("Noon works.");
    // The copy goes from the same account, in the same thread.
    await expect.element(editor.getByRole("button", { name: /From/ })).not.toBeInTheDocument();
    await expect.element(editor).toMatchTextContent(/From\s*studio@example\.com/);
    await userEvent.fill(editor.getByRole("textbox", { name: "Message" }), "1 pm works.");
    await userEvent.click(editor.getByRole("button", { name: "Send" }));

    const again = () =>
      calls.filter(
        (call) =>
          call.method === "POST" &&
          call.url === `/api/v0/outgoing-messages/${saved("failed").id}/send-again`,
      );
    await expect.poll(() => again()).toHaveLength(1);
    expect(again()[0].body).toEqual({
      to: ["ana@example.com"],
      cc: [],
      bcc: [],
      subject: "Re: Lunch",
      body: "1 pm works.",
    });
    expect(again()[0].headers.has("Idempotency-Key")).toBe(false);
  });

  it("offers no Edit and send or Dismiss for a refused reply the API allows neither", async () => {
    await page.viewport(1440, 900);
    const refused: OutgoingMessage = { ...saved("failed", "refused"), allowedActions: [] };
    serve({
      reply: () => Response.json({ data: refused }, { status: created }),
      statuses: [refused],
    });
    await show();
    await userEvent.click(page.getByRole("button", { name: "Reply", exact: true }));
    await userEvent.keyboard("Noon works.");
    await userEvent.click(page.getByRole("button", { name: "Send" }));

    await expect.element(page.getByText(/Gmail refused this message/)).toBeVisible();
    await expect
      .element(page.getByRole("button", { name: "Edit and send" }))
      .not.toBeInTheDocument();
    await expect.element(page.getByRole("button", { name: "Dismiss" })).not.toBeInTheDocument();
  });

  it("keeps the reply when the window crosses the phone width, both ways", async () => {
    await page.viewport(1024, 800);
    serve();
    await show();

    await userEvent.click(page.getByRole("button", { name: "Reply", exact: true }));
    await userEvent.keyboard("Noon works.");
    await page.viewport(390, 800);
    await expect.element(page.getByRole("textbox", { name: "Message" })).toHaveValue("Noon works.");
    await page.viewport(1024, 800);

    await expect.element(page.getByRole("textbox", { name: "Message" })).toHaveValue("Noon works.");
    // Inline at every width, so nothing moves or remounts.
    await expect.element(page.getByRole("dialog")).not.toBeInTheDocument();
  });

  it("asks before discarding a typed address", async () => {
    serve();
    await show();

    await userEvent.click(page.getByRole("button", { name: "Reply", exact: true }));
    await userEvent.click(page.getByRole("textbox", { name: "To" }));
    // Leaving the field makes the address a chip.
    await userEvent.keyboard("cy@example.com{Tab}");
    await userEvent.click(page.getByRole("button", { name: "Discard" }));

    await expect
      .element(page.getByRole("alertdialog", { name: "Discard this reply?" }))
      .toBeVisible();
  });

  it("sends with Ctrl+Enter from an address still being typed, keeping it", async () => {
    await page.viewport(1440, 900);
    const calls = serve();
    await show();

    await userEvent.click(page.getByRole("button", { name: "Reply", exact: true }));
    await userEvent.keyboard("Noon works.");
    await userEvent.click(page.getByRole("textbox", { name: "To" }));
    await userEvent.keyboard("cy@example.com{Control>}{Enter}{/Control}");

    await expect.poll(() => calls.filter((call) => call.method === "POST")).toHaveLength(1);
    const post = calls.find((call) => call.method === "POST");
    expect(post?.body).toMatchObject({ to: ["ana@example.com", "cy@example.com"] });
  });

  it("says when the status can't be read again, with Try again", async () => {
    await page.viewport(1440, 900);
    serve({ statuses: [saved("sending")], failReadsFrom: 1 });
    await show();

    await userEvent.click(page.getByRole("button", { name: "Reply", exact: true }));
    await userEvent.keyboard("Noon works.{Control>}{Enter}{/Control}");

    const status = page.getByRole("region", { name: "Your reply" });
    await expect
      .element(status.getByRole("button", { name: "Try again" }), { timeout: 8_000 })
      .toBeVisible();
    await expect.element(status).toMatchTextContent("may be out of date");
  });

  it("answers the message it opened on, though a newer one arrives", async () => {
    await page.viewport(1440, 900);
    const calls = serve();
    await show();

    await userEvent.click(page.getByRole("button", { name: "Reply", exact: true }));
    await userEvent.click(page.getByRole("button", { name: "Newer message arrives" }));
    await userEvent.click(page.getByRole("textbox", { name: "Message" }));
    await userEvent.keyboard("Noon works.{Control>}{Enter}{/Control}");

    await expect.poll(() => calls.filter((call) => call.method === "POST")).toHaveLength(1);
    expect(calls.find((call) => call.method === "POST")?.url).toBe(
      `/api/v0/messages/${answered.id}/replies`,
    );
  });

  it("moves focus to From when the server refuses the account", async () => {
    await page.viewport(1440, 900);
    serve({
      reply: () =>
        problem(422, "/api/v0/problems/validation-error", {
          errors: [{ location: "body.connectedAccountId", message: "Choose another account." }],
        }),
    });
    await show();

    await userEvent.click(page.getByRole("button", { name: "Reply", exact: true }));
    await userEvent.keyboard("Noon works.{Control>}{Enter}{/Control}");

    await expect.element(page.getByRole("button", { name: /From/ })).toHaveFocus();
    await expect.element(page.getByText("Choose another account.")).toBeVisible();
  });

  it("offers no reply to a message whose ID isn't one", async () => {
    await page.viewport(1440, 900);
    serve();
    await show({ ...answered, id: "../outgoing-messages" });

    await expect
      .element(page.getByRole("button", { name: "Reply", exact: true }))
      .not.toBeInTheDocument();
    await expect.element(page.getByText("This message can't be answered here.")).toBeVisible();
  });

  it("opens inline on a phone too, where the conversation stays a scroll away", async () => {
    await page.viewport(390, 800);
    serve();
    await show();

    await userEvent.click(page.getByRole("button", { name: "Reply", exact: true }));

    // In the page, not a full-screen dialog over it.
    await expect.element(page.getByRole("form", { name: "Reply" })).toBeVisible();
    await expect.element(page.getByRole("dialog")).not.toBeInTheDocument();
    await expect.element(page.getByRole("heading", { name: "Lunch" })).toBeInTheDocument();
    await expect.element(page.getByRole("textbox", { name: "Message" })).toHaveFocus();
  });
});
