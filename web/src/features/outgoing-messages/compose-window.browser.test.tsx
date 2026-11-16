import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import type { ListedAccount, OutgoingMessage } from "@/api/generated/model";
import { Button } from "@/components/ui/button";
import { LeaveWarning } from "@/components/leave-warning";
import { clearAccountCache } from "@/features/auth/session-cache";
import { appRouter, memberSession } from "@/test/app-router";
import { ComposeProvider, ComposeStore, useCompose } from "./compose-store";
import { ComposeWindow } from "./compose-window";
import { MessageStatus } from "./status";
import { editNote, leaveText } from "./text";

const ok = 200;
const created = 201;
const notFound = 404;

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
  outstanding: false,
  reply: false,
  allowedActions: [],
};

// The API as serve stubs it: accounts answers the accounts list; sent is
// what Send Message saves, which a read of it answers; again is the copy
// Send Message Again saves, which a read of it answers; outbox answers a
// read of the Outbox, which gives their status.
type Api = {
  accounts: () => ListedAccount[];
  sent: OutgoingMessage;
  again: OutgoingMessage;
  outbox: () => Response;
};

// serve stubs the API with api's answers, by default the member's one
// account, and Send Message saving saved, which reads back as saved and
// is the Outbox's one message.
function serve(api: Partial<Api> = {}): void {
  const { accounts = () => [studio], sent = saved } = api;
  const outbox =
    api.outbox ?? (() => Response.json({ data: { messages: [sent] } }, { status: ok }));
  vi.stubGlobal(
    "fetch",
    vi.fn<(url: string, init?: RequestInit) => Promise<Response>>(async (url, init = {}) => {
      const method = init.method ?? "GET";
      if (url === "/api/v0/connected-accounts")
        return Response.json(
          { data: { accounts: accounts(), syncStopped: false } },
          { status: ok },
        );
      if (method === "POST" && url === "/api/v0/outgoing-messages")
        return Response.json({ data: sent }, { status: created });
      if (url === `/api/v0/outgoing-messages/${sent.id}`)
        return Response.json({ data: sent }, { status: ok });
      if (
        api.again &&
        method === "POST" &&
        url === `/api/v0/outgoing-messages/${sent.id}/send-again`
      )
        return Response.json({ data: api.again }, { status: created });
      if (api.again && url === `/api/v0/outgoing-messages/${api.again.id}`)
        return Response.json({ data: api.again }, { status: ok });
      if (url === "/api/v0/outgoing-messages") return outbox();
      return Response.json({}, { status: notFound });
    }),
  );
}

// OpenButton opens the compose window, as the sidebar's Compose does.
function OpenButton(): React.ReactElement {
  const { store } = useCompose();
  return <Button onPress={() => store.open()}>Compose</Button>;
}

// A message Gmail refused, whose status offers Edit and send.
const refused: OutgoingMessage = {
  ...saved,
  status: "failed",
  failure: "refused",
  allowedActions: ["send-again"],
  sentAt: null,
  subject: "Refused",
};

// sendAgainCalls returns the page's calls to Send Message Again for
// refused, as fetch received them.
function sendAgainCalls() {
  return vi
    .mocked(fetch)
    .mock.calls.filter(([url]) => url === `/api/v0/outgoing-messages/${refused.id}/send-again`);
}

// show renders a page with Compose and the compose window, as the signed-in
// layout draws them, inside a router with a second page, /elsewhere. The
// page warns before it is left when leaving is "warns", e.g. with a reply
// being typed. It returns the store and the router.
async function show(leaving: "quiet" | "warns" = "quiet", status: OutgoingMessage = refused) {
  const store = new ComposeStore();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const root = createRootRoute({
    component: () => (
      <ComposeProvider value={store}>
        <Page />
        <ComposeWindow />
      </ComposeProvider>
    ),
  });
  const home = createRoute({
    getParentRoute: () => root,
    path: "/",
    component: () => (
      <main>
        <h1 tabIndex={-1}>Inbox</h1>
        <OpenButton />
        <LeaveWarning active={leaving === "warns"} text={leaveText("typed")} />
        <MessageStatus
          message={status}
          accounts={[studio]}
          onSentAgain={() => {}}
          onUnauthorized={() => {}}
          look="pane"
        />
      </main>
    ),
  });
  const elsewhere = createRoute({
    getParentRoute: () => root,
    path: "/elsewhere",
    component: () => <h1>Elsewhere</h1>,
  });
  const router = createRouter({
    routeTree: root.addChildren([home, elsewhere]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  await render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return { store, router, client };
}

// Page is the page behind the window, inert while the window covers it.
function Page(): React.ReactElement {
  const { state } = useCompose();
  return (
    <div inert={state.covering} className="contents">
      <Outlet />
    </div>
  );
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
    serve();
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("opens docked as a named region, with focus in From", async () => {
    await show();
    await userEvent.click(page.getByRole("button", { name: "Compose" }));

    const window = page.getByRole("region", { name: "New message" });
    await expect.element(window).toBeVisible();
    await expect.element(window.getByRole("button", { name: /From/ })).toHaveFocus();
  });

  it("keeps the text while minimised, and comes back to the field it left", async () => {
    await show();
    await userEvent.click(page.getByRole("button", { name: "Compose" }));
    await userEvent.type(page.getByRole("textbox", { name: "Subject" }), "Lunch");

    await userEvent.click(page.getByRole("button", { name: "Minimise", exact: true }));
    await expect.element(page.getByRole("textbox", { name: "Subject" })).not.toBeInTheDocument();
    await expect.element(page.getByRole("button", { name: "Lunch" })).toHaveFocus();

    await userEvent.click(page.getByRole("button", { name: "Restore", exact: true }));
    await expect.element(page.getByRole("textbox", { name: "Subject" })).toHaveValue("Lunch");
    await expect.element(page.getByRole("textbox", { name: "Subject" })).toHaveFocus();
  });

  it("covers the page at full screen, and Escape docks it again without closing", async () => {
    await show();
    await userEvent.click(page.getByRole("button", { name: "Compose" }));
    await userEvent.click(page.getByRole("button", { name: "Full screen", exact: true }));

    await expect.element(page.getByRole("dialog", { name: "New message" })).toBeVisible();
    expect(document.querySelector("[inert]")).not.toBeNull();

    await userEvent.keyboard("{Escape}");
    await expect.element(page.getByRole("region", { name: "New message" })).toBeVisible();
    expect(document.querySelector("[inert]")).toBeNull();
  });

  it("does nothing on Escape while docked", async () => {
    await show();
    await userEvent.click(page.getByRole("button", { name: "Compose" }));
    await userEvent.keyboard("{Escape}");
    await expect.element(page.getByRole("region", { name: "New message" })).toBeVisible();
  });

  it("stays while the member moves to another page", async () => {
    const { router } = await show();
    await userEvent.click(page.getByRole("button", { name: "Compose" }));
    await userEvent.type(page.getByRole("textbox", { name: "Subject" }), "Lunch");

    await router.navigate({ href: "/elsewhere" });
    await expect.element(page.getByRole("heading", { name: "Elsewhere" })).toBeVisible();
    await expect.element(page.getByRole("textbox", { name: "Subject" })).toHaveValue("Lunch");
  });

  it("asks before Close drops typed text, then gives focus back to Compose", async () => {
    await show();
    await userEvent.click(page.getByRole("button", { name: "Compose" }));
    await userEvent.type(page.getByRole("textbox", { name: "Subject" }), "Lunch");

    await userEvent.click(page.getByRole("button", { name: "Close", exact: true }));
    await press("Keep editing");
    await expect.element(page.getByRole("textbox", { name: "Subject" })).toHaveValue("Lunch");

    await userEvent.click(page.getByRole("button", { name: "Close", exact: true }));
    await press("Discard");
    await expect.element(page.getByRole("region", { name: "Lunch" })).not.toBeInTheDocument();
    await expect.element(page.getByRole("button", { name: "Compose" })).toHaveFocus();
  });

  it("closes an empty window at once", async () => {
    await show();
    await userEvent.click(page.getByRole("button", { name: "Compose" }));
    await userEvent.click(page.getByRole("button", { name: "Close", exact: true }));
    await expect.element(page.getByRole("region", { name: "New message" })).not.toBeInTheDocument();
  });

  it("brings back the message being written when Compose is pressed again", async () => {
    const { store } = await show();
    await userEvent.click(page.getByRole("button", { name: "Compose" }));
    await userEvent.type(page.getByRole("textbox", { name: "Subject" }), "Lunch");
    await userEvent.click(page.getByRole("button", { name: "Minimise", exact: true }));

    store.open();
    await expect.element(page.getByRole("textbox", { name: "Subject" })).toHaveValue("Lunch");
  });

  it("asks before Edit and send replaces the message being written", async () => {
    const { store } = await show();
    await userEvent.click(page.getByRole("button", { name: "Compose" }));
    await userEvent.type(page.getByRole("textbox", { name: "Subject" }), "Mine");

    store.open({
      ...saved,
      status: "failed",
      failure: "refused",
      subject: "Refused",
      allowedActions: ["send-again"],
    });
    await press("Keep writing");
    await expect.element(page.getByRole("textbox", { name: "Subject" })).toHaveValue("Mine");

    store.open({
      ...saved,
      status: "failed",
      failure: "refused",
      subject: "Refused",
      allowedActions: ["send-again"],
    });
    await press("Replace");
    await expect.element(page.getByRole("textbox", { name: "Subject" })).toHaveValue("Refused");
  });

  it("shows the sent message's status, then closes 3 s after Gmail takes it", async () => {
    await show();
    await userEvent.click(page.getByRole("button", { name: "Compose" }));
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
    await expect.element(page.getByRole("button", { name: "Compose" })).toHaveFocus();
  });

  it("opens from a status's Edit and send with that message's content, from its account", async () => {
    await show();
    await userEvent.click(page.getByRole("button", { name: "Edit and send" }));

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
    serve({ again: copy });
    await show();
    await userEvent.click(page.getByRole("button", { name: "Edit and send" }));
    await userEvent.fill(page.getByRole("textbox", { name: "Subject" }), "Refused, fixed");
    await userEvent.click(page.getByRole("button", { name: "Send" }));

    await expect.poll(() => sendAgainCalls()).toHaveLength(1);
    const [, init] = sendAgainCalls()[0];
    expect(typeof init?.body === "string" ? JSON.parse(init.body) : undefined).toEqual({
      to: ["ana@example.com"],
      cc: [],
      bcc: [],
      subject: "Refused, fixed",
      body: "Noon?",
    });
    expect(new Headers(init?.headers).has("Idempotency-Key")).toBe(false);
    await expect.element(page.getByRole("textbox", { name: "Message" })).not.toBeInTheDocument();
    await expect
      .element(page.getByRole("region", { name: "Refused, fixed" }))
      .toMatchTextContent("Sent to ana@example.com");
  });

  it("keeps a refused reply's subject, which the API keeps", async () => {
    await show("quiet", { ...refused, reply: true, subject: "Re: Lunch" });
    await userEvent.click(page.getByRole("button", { name: "Edit and send" }));

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
    await show("quiet", { ...refused, reply: true, subject });
    await userEvent.click(page.getByRole("button", { name: "Edit and send" }));

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
    const router = await appRouter("/compose", memberSession);
    expect(router.state.location.pathname).toBe("/mail/inbox");
    expect(router.history.length).toBe(1);
    expect(router.options.context.compose.snapshot().message).not.toBeNull();
  });

  it("closes when the member's data is cleared, e.g. on sign-out", async () => {
    const { store } = await show();
    await userEvent.click(page.getByRole("button", { name: "Compose" }));
    await userEvent.type(page.getByRole("textbox", { name: "Subject" }), "Lunch");

    clearAccountCache(new QueryClient());
    await expect.element(page.getByRole("region", { name: "Lunch" })).not.toBeInTheDocument();
    expect(store.snapshot().message).toBeNull();
  });

  it("keeps the editor when a later read finds no account able to send", async () => {
    let accounts = [studio];
    serve({ accounts: () => accounts });
    const { client } = await show();
    await userEvent.click(page.getByRole("button", { name: "Compose" }));
    await userEvent.type(page.getByRole("textbox", { name: "Subject" }), "Lunch");

    accounts = [
      {
        ...studio,
        status: "needs-reconnect",
        allowedActions: ["reconnect", "rename", "disconnect"],
      },
    ];
    await client.invalidateQueries();
    // The new read has drawn: From now marks the account.
    await userEvent.click(page.getByRole("button", { name: /From/ }));
    await expect.element(page.getByRole("option", { name: /needs reconnecting/ })).toBeVisible();
    await userEvent.keyboard("{Escape}");
    await expect.element(page.getByRole("textbox", { name: "Subject" })).toHaveValue("Lunch");
  });

  it("says the accounts can't send yet when the server lists none able to", async () => {
    // Usable, as when no Google client is set up: only allowedActions says.
    serve({ accounts: () => [{ ...studio, allowedActions: ["rename", "disconnect"] }] });
    await show();
    await userEvent.click(page.getByRole("button", { name: "Compose" }));

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

  it("says the session ended when a read of its status gets a 401", async () => {
    serve({
      sent: { ...saved, status: "sending", sentAt: null },
      outbox: () =>
        Response.json(
          { type: "/api/v0/problems/unauthenticated", status: 401, title: "", detail: "" },
          { status: 401, headers: { "Content-Type": "application/problem+json" } },
        ),
    });
    await show();
    await userEvent.click(page.getByRole("button", { name: "Compose" }));
    await userEvent.click(page.getByRole("button", { name: /From/ }));
    await userEvent.click(page.getByRole("option", { name: "studio@example.com Studio" }));
    await userEvent.type(page.getByRole("textbox", { name: "To" }), "ana@example.com,");
    await userEvent.click(page.getByRole("button", { name: "Send" }));

    await expect
      .element(page.getByRole("alert"), { timeout: 5_000 })
      .toMatchTextContent("You're signed out");
  });

  it("follows the copy Send again saves, reading the Outbox again", async () => {
    const failed: OutgoingMessage = {
      ...saved,
      status: "failed",
      failure: "gave-up",
      sentAt: null,
      allowedActions: ["send-again"],
    };
    // Its own read says sending; only the Outbox's says it went out.
    const copy: OutgoingMessage = {
      ...saved,
      id: "019a0000-0000-7000-8000-0000000000dd",
      status: "sending",
      sentAt: null,
    };
    let tried = false;
    serve({
      sent: failed,
      again: copy,
      outbox: () => {
        const messages = tried
          ? [
              { ...copy, status: "sent", sentAt: saved.sentAt },
              { ...failed, sentAgainAs: copy.id },
            ]
          : [failed];
        return Response.json({ data: { messages } }, { status: ok });
      },
    });
    await show();
    await userEvent.click(page.getByRole("button", { name: "Compose" }));
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
    serve({
      sent: { ...saved, status: "sending", sentAt: null },
      outbox: () =>
        Response.json(
          { type: "/api/v0/problems/internal-error", status: 500, title: "", detail: "" },
          { status: 500, headers: { "Content-Type": "application/problem+json" } },
        ),
    });
    await show();
    await userEvent.click(page.getByRole("button", { name: "Compose" }));
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
      const { router } = await show();
      await userEvent.click(page.getByRole("button", { name: "Compose" }));

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
      const { router } = await show();
      await userEvent.click(page.getByRole("button", { name: "Compose" }));
      await userEvent.type(page.getByRole("textbox", { name: "Subject" }), "Lunch");

      void router.navigate({ href: "/elsewhere" });
      await press("Keep editing");
      await expect.element(page.getByRole("textbox", { name: "Subject" })).toHaveValue("Lunch");

      void router.navigate({ href: "/elsewhere" });
      await press("Discard");
      await expect.element(page.getByRole("dialog", { name: "Lunch" })).not.toBeInTheDocument();
      await expect.element(page.getByRole("heading", { name: "Elsewhere" })).toBeVisible();
    });

    it("keeps Tab going round its own controls", async () => {
      await show();
      await userEvent.click(page.getByRole("button", { name: "Compose" }));
      const window = page.getByRole("dialog", { name: "New message" });
      await expect.element(window).toBeVisible();

      // Discard is its last control.
      window.getByRole("button", { name: "Discard" }).element().focus();
      await userEvent.tab();
      await expect.element(window.getByRole("button", { name: "Close" })).toHaveFocus();
    });

    it("gives focus back to Compose when Back closes it", async () => {
      const { router } = await show();
      await userEvent.click(page.getByRole("button", { name: "Compose" }));
      await expect.poll(() => router.history.location.state.composeWindow).toBeDefined();

      router.history.back();
      await expect.element(page.getByRole("button", { name: "Compose" })).toHaveFocus();
    });

    it("keeps its one history entry when Edit and send replaces the message", async () => {
      const { router, store } = await show();
      await userEvent.click(page.getByRole("button", { name: "Compose" }));
      await expect.poll(() => router.history.location.state.composeWindow).toBeDefined();
      const entries = router.history.length;

      store.open(refused);
      await expect.element(page.getByRole("textbox", { name: "Subject" })).toHaveValue("Refused");
      expect(router.history.length).toBe(entries);

      await userEvent.click(page.getByRole("button", { name: "Close", exact: true }));
      await expect.element(page.getByRole("dialog", { name: "Refused" })).not.toBeInTheDocument();
      await expect.poll(() => router.history.location.state.composeWindow).toBeUndefined();
    });

    it("takes its history entry and closes past a page that warns before it is left", async () => {
      const { router } = await show("warns");
      await userEvent.click(page.getByRole("button", { name: "Compose" }));
      await expect.poll(() => router.history.location.state.composeWindow).toBeDefined();

      await userEvent.click(page.getByRole("button", { name: "Close", exact: true }));
      await expect
        .element(page.getByRole("dialog", { name: "New message" }))
        .not.toBeInTheDocument();
      await expect.poll(() => router.history.location.state.composeWindow).toBeUndefined();
      expect(page.getByRole("alertdialog").query()).toBeNull();
    });

    it("steps back through history when closed", async () => {
      const { router } = await show();
      await userEvent.click(page.getByRole("button", { name: "Compose" }));
      await expect.poll(() => router.history.location.state.composeWindow).toBeDefined();

      await userEvent.click(page.getByRole("button", { name: "Close", exact: true }));
      await expect
        .element(page.getByRole("dialog", { name: "New message" }))
        .not.toBeInTheDocument();
      await expect.poll(() => router.history.location.state.composeWindow).toBeUndefined();
    });
  });
});
