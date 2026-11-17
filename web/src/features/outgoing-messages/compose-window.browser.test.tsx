import { RouterProvider } from "@tanstack/react-router";
import { afterEach, beforeEach, describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { cleanup, render } from "vitest-browser-react";
import type {
  ConversationMessage,
  ConversationRow,
  ListedAccount,
  ListedSessions,
  OutgoingMessage,
  TotpStatus,
} from "@/api/generated/model";
import { clearAccountCache } from "@/features/auth/session-cache";
import { appRouter } from "@/test/app-router";
import { conversation, mailApi, type MailApi } from "@/test/mail-api";
import { data, fakeServer, memberSession, problem } from "@/test/server";
import { ComposeStore } from "./compose-store";
import { editNote } from "./text";

const created = 201;
const notFound = 404;
const fault = 500;

const studio: ListedAccount = {
  id: 7,
  provider: "gmail",
  email: "studio@example.com",
  name: "Studio",
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

// The message the server saves, which Gmail has already taken.
const saved: OutgoingMessage = {
  id: "019a0000-0000-7000-8000-0000000000cc",
  status: "sent",
  failure: null,
  from: studio.email,
  connectedAccountId: studio.id,
  to: ["ana@example.com"],
  cc: [],
  bcc: [],
  subject: "Lunch",
  body: "Noon?",
  createdAt: "2026-11-16T08:00:00Z",
  sentAt: "2026-11-16T08:00:01Z",
  sentAgainAs: null,
  dismissedAt: null,
  outstanding: false,
  reply: false,
  allowedActions: [],
};

const twoFactorOff: TotpStatus = { status: "off", backupCodesLeft: 0, backupCodesWaiting: false };

// The member's one signed-in browser, this one.
const browsers: ListedSessions = {
  sessions: [
    {
      id: "0193c8a0-0000-7000-8000-000000000001",
      browser: "Firefox",
      os: "Linux",
      signedInAt: "2026-11-01T10:00:00Z",
      signedInFrom: "203.0.113.7",
      lastActiveAt: "2026-11-01T10:00:00Z",
      current: true,
    },
  ],
};

// message returns the one message of row, from Bo, for a reply to answer.
function message(row: ConversationRow): ConversationMessage {
  return {
    id: "00000000-0000-4000-8000-000000000901",
    date: row.date,
    subject: row.subject,
    preview: "Hello",
    from: [{ name: "Bo Tan", address: "bo@example.com" }],
    to: [{ name: "", address: "studio@example.com" }],
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
}

// The API's answers as open sets them: conversation, when given, is the
// member's one conversation, with one message; accounts are the member's; sent is
// what Send Message saves, which a read of it answers; again is the copy
// Send Message Again saves, which a read of it answers; outbox answers a
// read of the Outbox, which gives their status, by default with sent alone.
type Answers = {
  conversation: ConversationRow;
  accounts: ListedAccount[];
  sent: OutgoingMessage;
  again: OutgoingMessage;
  outbox: () => Response;
};

// A message Gmail refused, whose status offers Edit and send.
const refused: OutgoingMessage = {
  ...saved,
  status: "failed",
  failure: "refused",
  allowedActions: ["send-again", "dismiss"],
  sentAt: null,
  subject: "Refused",
};

// The Outbox's page with refused open in its reading pane, whose status
// offers Edit and send.
const refusedPage = `/mail/outbox/${refused.id}`;

let api: MailApi;
let current: Awaited<ReturnType<typeof appRouter>> | undefined;

afterEach(async () => {
  await cleanup();
  if (current) clearAccountCache(current.options.context.queryClient);
  current = undefined;
});

// open renders the app at address, the Inbox by default, with answers, by
// default for a member whose one account is Studio, Send Message saving
// saved. It returns the app's router; api is its server.
async function open(address = "/mail/inbox", answers: Partial<Answers> = {}) {
  const { accounts = [studio], sent = saved, again, conversation: row } = answers;
  api = mailApi(row ? [row] : [], accounts);
  const { server } = api;
  if (row) {
    server.on("GET", "/api/v0/conversations/{id}", ({ params }, earlier) =>
      params.id === row.id
        ? data({ conversation: row, messages: [message(row)], providerUrl: null })
        : earlier(),
    );
    server.on("GET", "/api/v0/messages/{id}/content", () =>
      data({ remote: "none", attachments: [], bodies: [] }),
    );
  }
  server.on(
    "GET",
    "/api/v0/outgoing-messages",
    answers.outbox ?? (() => data({ messages: [sent] })),
  );
  server.on("POST", "/api/v0/outgoing-messages", () => data(sent, created));
  server.on("GET", "/api/v0/outgoing-messages/{id}", ({ params }) => {
    if (params.id === sent.id) return data(sent);
    if (params.id === again?.id) return data(again);
    return problem(notFound, "/api/v0/problems/not-found");
  });
  server.on("POST", "/api/v0/outgoing-messages/{id}/send-again", ({ params }) =>
    again && params.id === sent.id
      ? data(again, created)
      : problem(notFound, "/api/v0/problems/not-found"),
  );
  const router = await appRouter(address, server);
  await render(<RouterProvider router={router} />);
  current = router;
  return router;
}

// sendAgainCalls returns the app's calls to Send Message Again for
// refused, as the server got them.
function sendAgainCalls() {
  return api.server.requests.filter(
    (asked) =>
      asked.method === "POST" &&
      asked.url.pathname === `/api/v0/outgoing-messages/${refused.id}/send-again`,
  );
}

// editAndSend returns the open message's Edit and send, in the reading
// pane's status.
function editAndSend() {
  return page.getByRole("button", { name: "Edit and send" });
}

// compose returns the mail sidebar's Compose, on a wide window.
function compose() {
  return page.getByRole("navigation", { name: "Mail" }).getByRole("button", { name: "Compose" });
}

// composeInDrawer opens the sidebar's drawer, as a phone has it, and
// presses its Compose.
async function composeInDrawer(): Promise<void> {
  await userEvent.click(page.getByRole("button", { name: "Open sidebar" }));
  await userEvent.click(page.getByRole("button", { name: "Compose" }));
}

// press presses an alert dialog's button named name, after the dialog's
// guard against a double click's second press (AlertDialogAction).
async function press(name: string): Promise<void> {
  const button = page.getByRole("alertdialog").getByRole("button", { name });
  await expect.element(button).toBeVisible();
  await new Promise((resolve) => setTimeout(resolve, 600));
  await userEvent.click(button);
}

describe("ComposeWindow", () => {
  beforeEach(async () => {
    await page.viewport(1280, 800);
  });

  it("opens docked as a named region, with focus in From", async () => {
    await open();
    await userEvent.click(compose());

    const window = page.getByRole("region", { name: "New message" });
    await expect.element(window).toBeVisible();
    await expect.element(window.getByRole("button", { name: /From/ })).toHaveFocus();
  });

  it("keeps the text while minimised, and comes back to the field it left", async () => {
    await open();
    await userEvent.click(compose());
    await userEvent.type(page.getByRole("textbox", { name: "Subject" }), "Lunch");

    await userEvent.click(page.getByRole("button", { name: "Minimise", exact: true }));
    await expect.element(page.getByRole("textbox", { name: "Subject" })).not.toBeInTheDocument();
    await expect.element(page.getByRole("button", { name: "Lunch" })).toHaveFocus();

    await userEvent.click(page.getByRole("button", { name: "Restore", exact: true }));
    await expect.element(page.getByRole("textbox", { name: "Subject" })).toHaveValue("Lunch");
    await expect.element(page.getByRole("textbox", { name: "Subject" })).toHaveFocus();
  });

  it("covers the page at full screen, and Escape docks it again without closing", async () => {
    await open();
    await userEvent.click(compose());
    await userEvent.click(page.getByRole("button", { name: "Full screen", exact: true }));

    await expect.element(page.getByRole("dialog", { name: "New message" })).toBeVisible();
    expect(document.querySelector("[inert]")).not.toBeNull();

    await userEvent.keyboard("{Escape}");
    await expect.element(page.getByRole("region", { name: "New message" })).toBeVisible();
    expect(document.querySelector("[inert]")).toBeNull();
  });

  it("does nothing on Escape while docked", async () => {
    await open();
    await userEvent.click(compose());
    await userEvent.keyboard("{Escape}");
    await expect.element(page.getByRole("region", { name: "New message" })).toBeVisible();
  });

  it("stays while the member moves to another page", async () => {
    const router = await open();
    // What Security reads.
    api.server.on("GET", "/api/v0/totp", () => data(twoFactorOff));
    api.server.on("GET", "/api/v0/sessions", () => data(browsers));
    await userEvent.click(compose());
    await userEvent.type(page.getByRole("textbox", { name: "Subject" }), "Lunch");

    await router.navigate({ href: "/settings/security" });
    await expect.element(page.getByRole("heading", { name: "Security" })).toBeVisible();
    await expect.element(page.getByRole("textbox", { name: "Subject" })).toHaveValue("Lunch");
  });

  it("asks before Close drops typed text, then gives focus back to Compose", async () => {
    await open();
    await userEvent.click(compose());
    await userEvent.type(page.getByRole("textbox", { name: "Subject" }), "Lunch");

    await userEvent.click(page.getByRole("button", { name: "Close", exact: true }));
    await press("Keep editing");
    await expect.element(page.getByRole("textbox", { name: "Subject" })).toHaveValue("Lunch");

    await userEvent.click(page.getByRole("button", { name: "Close", exact: true }));
    await press("Discard");
    await expect.element(page.getByRole("region", { name: "Lunch" })).not.toBeInTheDocument();
    await expect.element(compose()).toHaveFocus();
  });

  it("closes an empty window at once", async () => {
    await open();
    await userEvent.click(compose());
    await userEvent.click(page.getByRole("button", { name: "Close", exact: true }));
    await expect.element(page.getByRole("region", { name: "New message" })).not.toBeInTheDocument();
  });

  it("brings back the message being written when Compose is pressed again", async () => {
    await open();
    await userEvent.click(compose());
    await userEvent.type(page.getByRole("textbox", { name: "Subject" }), "Lunch");
    await userEvent.click(page.getByRole("button", { name: "Minimise", exact: true }));

    await userEvent.click(compose());
    await expect.element(page.getByRole("textbox", { name: "Subject" })).toHaveValue("Lunch");
  });

  it("closes when the member's data is cleared, e.g. on sign-out", async () => {
    const router = await open();
    await userEvent.click(compose());
    await userEvent.type(page.getByRole("textbox", { name: "Subject" }), "Lunch");

    clearAccountCache(router.options.context.queryClient);
    await expect.element(page.getByRole("region", { name: "Lunch" })).not.toBeInTheDocument();
    expect(router.options.context.compose.snapshot().message).toBeNull();
  });

  it("keeps the editor when a later read finds no account able to send", async () => {
    const router = await open();
    await userEvent.click(compose());
    await userEvent.type(page.getByRole("textbox", { name: "Subject" }), "Lunch");

    api.accounts = [
      {
        ...studio,
        status: "needs-reconnect",
        allowedActions: ["reconnect", "rename", "disconnect"],
      },
    ];
    await router.options.context.queryClient.invalidateQueries();
    // The new read has drawn: From now marks the account.
    await userEvent.click(page.getByRole("button", { name: /From/ }));
    await expect.element(page.getByRole("option", { name: /needs reconnecting/ })).toBeVisible();
    await userEvent.keyboard("{Escape}");
    await expect.element(page.getByRole("textbox", { name: "Subject" })).toHaveValue("Lunch");
  });

  it("asks before Edit and send replaces the message being written", async () => {
    await open(refusedPage, { sent: refused });
    await userEvent.click(compose());
    await userEvent.type(page.getByRole("textbox", { name: "Subject" }), "Mine");

    await userEvent.click(editAndSend());
    await press("Keep writing");
    await expect.element(page.getByRole("textbox", { name: "Subject" })).toHaveValue("Mine");

    await userEvent.click(editAndSend());
    await press("Replace");
    await expect.element(page.getByRole("textbox", { name: "Subject" })).toHaveValue("Refused");
  });

  it("shows the sent message's status, then closes 3 s after Gmail takes it", async () => {
    await open();
    await userEvent.click(compose());
    await userEvent.click(page.getByRole("button", { name: /From/ }));
    await userEvent.click(page.getByRole("option", { name: "studio@example.com Studio" }));
    await userEvent.type(page.getByRole("textbox", { name: "To" }), "ana@example.com,");
    await userEvent.type(page.getByRole("textbox", { name: "Subject" }), "Lunch");
    await userEvent.click(page.getByRole("button", { name: "Send" }));

    const status = page
      .getByRole("region", { name: "Lunch" })
      .getByRole("region", { name: "Status" });
    await expect.element(status).toHaveFocus();
    await expect.element(status).toMatchTextContent("Sent to ana@example.com");
    // Away from the window, so nothing holds it open.
    await userEvent.unhover(status);

    await expect
      .element(page.getByRole("region", { name: "Lunch" }), { timeout: 5_000 })
      .not.toBeInTheDocument();
    await expect.element(compose()).toHaveFocus();
  });

  it("opens from a status's Edit and send with that message's content, from its account", async () => {
    await open(refusedPage, { sent: refused });
    await userEvent.click(editAndSend());

    const window = page.getByRole("region", { name: "Refused" });
    await expect.element(window.getByRole("textbox", { name: "Subject" })).toHaveValue("Refused");
    await expect.element(window).toMatchTextContent(editNote);
    // The copy goes from the same account: From is text, not a choice.
    await expect.element(window).toMatchTextContent(/From\s*studio@example\.com/);
    await expect.element(window.getByRole("button", { name: /From/ })).not.toBeInTheDocument();
    await expect.element(window.getByRole("textbox", { name: "Message" })).toHaveFocus();
  });

  it("sends Edit and send's edits through Send again, with no key, then shows the copy", async () => {
    const copy: OutgoingMessage = {
      ...saved,
      id: "019a0000-0000-7000-8000-0000000000dd",
      subject: "Refused, fixed",
    };
    await open(refusedPage, { sent: refused, again: copy });
    await userEvent.click(editAndSend());
    await userEvent.fill(page.getByRole("textbox", { name: "Subject" }), "Refused, fixed");
    await userEvent.click(page.getByRole("button", { name: "Send" }));

    await expect.poll(() => sendAgainCalls()).toHaveLength(1);
    const [asked] = sendAgainCalls();
    expect(asked.body).toEqual({
      to: ["ana@example.com"],
      cc: [],
      bcc: [],
      subject: "Refused, fixed",
      body: "Noon?",
    });
    expect(asked.headers.has("Idempotency-Key")).toBe(false);
    await expect.element(page.getByRole("textbox", { name: "Message" })).not.toBeInTheDocument();
    await expect
      .element(page.getByRole("region", { name: "Refused, fixed" }))
      .toMatchTextContent("Sent to ana@example.com");
  });

  it("keeps a refused reply's subject, which the API keeps", async () => {
    await open(refusedPage, { sent: { ...refused, reply: true, subject: "Re: Lunch" } });
    await userEvent.click(editAndSend());

    const window = page.getByRole("region", { name: "Re: Lunch" });
    await expect.element(window).toMatchTextContent(/Subject\s*Re: Lunch/);
    await expect.element(window.getByRole("textbox", { name: "Subject" })).not.toBeInTheDocument();
    // Two lines hold it whole, so nothing to unfold.
    await expect.element(window.getByRole("button", { name: "Show all" })).not.toBeInTheDocument();
  });

  // A reply's subject runs to 500 characters; 2 lines show, then Show all
  // unfolds the rest, which takes focus, as a message's details do.
  it("cuts a long fixed subject to 2 lines, with Show all to read it whole", async () => {
    const subject =
      `Re: ${"Quarterly review of the warehouse leases and the revised floor plans, ".repeat(6)}`.slice(
        0,
        500,
      );
    await open(refusedPage, { sent: { ...refused, reply: true, subject } });
    await userEvent.click(editAndSend());

    const showAll = page.getByRole("button", { name: "Show all" });
    // The window's title shows it too, first; the row's is the last.
    const text = page.getByText(subject, { exact: true }).last();
    await expect.element(showAll).toBeVisible();
    expect(text.element().scrollHeight).toBeGreaterThan(text.element().clientHeight);
    await userEvent.click(showAll);

    await expect.element(showAll).not.toBeInTheDocument();
    await expect.element(text).toHaveFocus();
    expect(text.element().scrollHeight).toBe(text.element().clientHeight);
  });

  it("opens from /compose over the Inbox, which history keeps in its place", async () => {
    const router = await appRouter("/compose", fakeServer(memberSession));
    expect(router.state.location.pathname).toBe("/mail/inbox");
    expect(router.history.length).toBe(1);
    expect(router.options.context.compose.snapshot().message).not.toBeNull();
  });

  it("says the accounts can't send yet when the server lists none able to", async () => {
    // Usable, as when no Google client is set up: only allowedActions says.
    await open("/mail/inbox", {
      accounts: [{ ...studio, allowedActions: ["rename", "disconnect"] }],
    });
    await userEvent.click(compose());

    const window = page.getByRole("region", { name: "New message" });
    await expect.element(window).toMatchTextContent("Your Gmail accounts can't send yet.");
    await expect
      .element(window.getByRole("link", { name: "Open Connected accounts" }))
      .toBeVisible();
    await expect.element(window.getByRole("textbox", { name: "Subject" })).not.toBeInTheDocument();
  });

  it("gives each window an id no later page load repeats", () => {
    // A reload makes a new store; a phone's history entry keeps the old id.
    const before = new ComposeStore();
    const after = new ComposeStore();
    before.open();
    after.open();
    expect(after.snapshot().message?.window).not.toBe(before.snapshot().message?.window);
  });

  it("follows the copy Send again saves, reading the Outbox again", async () => {
    const failed: OutgoingMessage = {
      ...saved,
      status: "failed",
      failure: "gave-up",
      sentAt: null,
      allowedActions: ["send-again", "dismiss"],
    };
    // Its own read says sending; only the Outbox's says it went out.
    const copy: OutgoingMessage = {
      ...saved,
      id: "019a0000-0000-7000-8000-0000000000dd",
      status: "sending",
      sentAt: null,
    };
    let tried = false;
    await open("/mail/inbox", {
      sent: failed,
      again: copy,
      outbox: () =>
        data({
          messages: tried
            ? [
                { ...copy, status: "sent", sentAt: saved.sentAt },
                { ...failed, sentAgainAs: copy.id },
              ]
            : [failed],
        }),
    });
    await userEvent.click(compose());
    await userEvent.click(page.getByRole("button", { name: /From/ }));
    await userEvent.click(page.getByRole("option", { name: "studio@example.com Studio" }));
    await userEvent.type(page.getByRole("textbox", { name: "To" }), "ana@example.com,");
    await userEvent.click(page.getByRole("button", { name: "Send" }));

    const window = page.getByRole("region", { name: "Lunch" });
    await expect.element(window.getByRole("button", { name: "Send again" })).toBeVisible();
    tried = true;
    await userEvent.click(window.getByRole("button", { name: "Send again" }));

    await expect
      .element(window.getByText("Sent to ana@example.com"), { timeout: 5_000 })
      .toBeVisible();
  });

  it("keeps the status, saying it may be out of date, when a reread fails", async () => {
    await open("/mail/inbox", {
      sent: { ...saved, status: "sending", sentAt: null },
      outbox: () => problem(fault, "/api/v0/problems/internal-error"),
    });
    await userEvent.click(compose());
    await userEvent.click(page.getByRole("button", { name: /From/ }));
    await userEvent.click(page.getByRole("option", { name: "studio@example.com Studio" }));
    await userEvent.type(page.getByRole("textbox", { name: "To" }), "ana@example.com,");
    await userEvent.click(page.getByRole("button", { name: "Send" }));

    await expect.element(page.getByText(/may be out of date/), { timeout: 5_000 }).toBeVisible();
    await expect.element(page.getByText("Sending to ana@example.com...")).toBeVisible();
    await expect.element(page.getByRole("button", { name: "Try again" })).toBeVisible();
  });

  describe("on a phone", () => {
    beforeEach(async () => {
      await page.viewport(390, 844);
    });

    it("is a full-screen dialog with Send at the top, and Back closes it", async () => {
      const router = await open();
      await composeInDrawer();

      const window = page.getByRole("dialog", { name: "New message" });
      await expect.element(window).toBeVisible();
      await expect.element(window.getByRole("button", { name: "Send" })).toBeVisible();
      await expect
        .element(window.getByRole("button", { name: "Minimise" }))
        .not.toBeInTheDocument();

      router.history.back();
      await expect.element(window).not.toBeInTheDocument();
    });

    // NOTE: TanStack's memory history asks its blockers only before a push
    // or a replace, never before Back, which the browser's history does;
    // leaving by a link takes the same way out of the window's entry.
    it("asks before leaving its entry drops typed text", async () => {
      const router = await open();
      await composeInDrawer();
      await userEvent.type(page.getByRole("textbox", { name: "Subject" }), "Lunch");

      void router.navigate({ href: "/mail/starred" });
      await press("Keep editing");
      await expect.element(page.getByRole("textbox", { name: "Subject" })).toHaveValue("Lunch");

      void router.navigate({ href: "/mail/starred" });
      await press("Discard");
      await expect.element(page.getByRole("dialog", { name: "Lunch" })).not.toBeInTheDocument();
      await expect.element(page.getByRole("heading", { name: "Starred" })).toBeVisible();
    });

    it("keeps Tab going round its own controls", async () => {
      await open();
      await composeInDrawer();
      const window = page.getByRole("dialog", { name: "New message" });
      await expect.element(window).toBeVisible();

      // Discard is its last control.
      window.getByRole("button", { name: "Discard" }).element().focus();
      await userEvent.tab();
      await expect.element(window.getByRole("button", { name: "Close" })).toHaveFocus();
    });

    // On a phone Compose sits in the drawer, which closes as it opens the
    // window, so the status's Edit and send is the opener that stays.
    it("gives focus back to what opened it when Back closes it", async () => {
      const router = await open(refusedPage, { sent: refused });
      await userEvent.click(editAndSend());
      await expect.poll(() => router.history.location.state.composeWindow).toBeDefined();

      router.history.back();
      await expect.element(editAndSend()).toHaveFocus();
    });

    it("keeps its one history entry when Edit and send replaces the message", async () => {
      // Gmail refuses the message, so the window's status offers Edit and send.
      const router = await open("/mail/inbox", { sent: refused });
      await composeInDrawer();
      await expect.poll(() => router.history.location.state.composeWindow).toBeDefined();
      const entries = router.history.length;
      await userEvent.click(page.getByRole("button", { name: /From/ }));
      await userEvent.click(page.getByRole("option", { name: "studio@example.com Studio" }));
      await userEvent.type(page.getByRole("textbox", { name: "To" }), "ana@example.com,");
      await userEvent.click(page.getByRole("button", { name: "Send" }));

      await userEvent.click(editAndSend());
      await expect.element(page.getByRole("textbox", { name: "Subject" })).toHaveValue("Refused");
      expect(router.history.length).toBe(entries);

      await userEvent.click(page.getByRole("button", { name: "Close", exact: true }));
      await expect.element(page.getByRole("dialog", { name: "Refused" })).not.toBeInTheDocument();
      await expect.poll(() => router.history.location.state.composeWindow).toBeUndefined();
    });

    it("takes its history entry and closes past a page that warns before it is left", async () => {
      // A conversation with a reply being typed warns before it is left.
      const row = conversation(1, { accountId: studio.id });
      const router = await open(`/mail/inbox/${row.id}`, { conversation: row });
      await userEvent.click(page.getByRole("button", { name: "Reply", exact: true }).last());
      await userEvent.type(page.getByRole("textbox", { name: "Message" }), "See you at nine.");
      // A phone's reading pane has no Compose, and a link to /compose
      // would leave the page: only the store opens the window here.
      router.options.context.compose.open();
      await expect.poll(() => router.history.location.state.composeWindow).toBeDefined();

      const window = page.getByRole("dialog", { name: "New message" });
      await userEvent.click(window.getByRole("button", { name: "Close", exact: true }));
      await expect.element(window).not.toBeInTheDocument();
      await expect.poll(() => router.history.location.state.composeWindow).toBeUndefined();
      expect(page.getByRole("alertdialog").query()).toBeNull();
    });

    it("steps back through history when closed", async () => {
      const router = await open();
      await composeInDrawer();
      await expect.poll(() => router.history.location.state.composeWindow).toBeDefined();

      await userEvent.click(page.getByRole("button", { name: "Close", exact: true }));
      await expect
        .element(page.getByRole("dialog", { name: "New message" }))
        .not.toBeInTheDocument();
      await expect.poll(() => router.history.location.state.composeWindow).toBeUndefined();
    });
  });
});
