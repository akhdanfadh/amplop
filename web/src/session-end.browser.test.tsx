import { RouterProvider } from "@tanstack/react-router";
import { afterEach, describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { cleanup, render } from "vitest-browser-react";
import type { ConversationMessage, ConversationRow } from "@/api/generated/model";
import { closeToasts } from "@/components/ui/toast";
import { clearAccountCache } from "@/features/auth/session-cache";
import { refreshNewest } from "@/features/mail/conversations";
import { appRouter } from "@/test/app-router";
import { conversation, listedAccount, mailApi, type MailApi } from "@/test/mail-api";
import { data, fakeServer, memberSession } from "@/test/server";

// The tests of the one rule for a session that ends on an open page
// (watchSessionEnd, web/README.md "Routes"), through the app's own pages.

let api: MailApi;
let current: Awaited<ReturnType<typeof appRouter>> | undefined;

afterEach(async () => {
  closeToasts();
  await cleanup();
  if (current) clearAccountCache(current.options.context.queryClient);
  current = undefined;
});

// The one message of row, from Bo, for the reply to answer.
function message(row: ConversationRow): ConversationMessage {
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
    unread: false,
    answers: null,
    attachments: false,
  };
}

// open renders the app at address, width wide, over row and one account,
// with row's message and its content, and returns its router.
async function open(address: string, row: ConversationRow, width = 1440) {
  await page.viewport(width, 900);
  api = mailApi([row], [listedAccount(1, "ana@example.com")]);
  api.server.on("GET", "/api/v0/conversations/{id}", ({ params }, earlier) =>
    params.id === row.id
      ? data({ conversation: row, messages: [message(row)], providerUrl: null })
      : earlier(),
  );
  api.server.on("GET", "/api/v0/messages/{id}/content", () =>
    data({ remote: "none", attachments: [], bodies: [] }),
  );
  const router = await appRouter(address, api.server);
  current = router;
  await render(<RouterProvider router={router} />);
  return router;
}

// endSession ends the session on the server, and has the page find out
// from its next read, as the mail list's refresh does.
async function endSession(router: Awaited<ReturnType<typeof appRouter>>): Promise<void> {
  api.signedOut = true;
  await refreshNewest(router.options.context.queryClient);
}

// press presses name in the dialog that asks, once it can take a press.
async function press(name: string): Promise<void> {
  const button = page.getByRole("alertdialog").getByRole("button", { name, exact: true });
  await expect.element(button).toBeVisible();
  // The dialog ignores a press within a double-click of opening.
  await new Promise((resolve) => setTimeout(resolve, 600));
  await userEvent.click(button);
}

const banner = () => page.getByRole("region", { name: "Signed out" });

describe("an ended session", () => {
  it("opens sign-in, which says why and returns here, with nothing to lose", async () => {
    const row = conversation(1);
    const router = await open("/mail/inbox", row);
    await expect.element(page.getByRole("row", { name: /Subject 1,/ })).toBeVisible();

    await endSession(router);

    await expect.element(page.getByText("Your session ended. Sign in to continue.")).toBeVisible();
    expect(router.state.location.pathname).toBe("/sign-in");
    expect(router.state.location.search).toEqual({ redirect: "/mail/inbox" });
  });

  it("keeps a typed reply to copy, then opens sign-in once it is discarded", async () => {
    const row = conversation(1);
    const router = await open(`/mail/inbox/${row.id}`, row);
    await userEvent.click(page.getByRole("button", { name: "Reply", exact: true }).last());
    const typed = page.getByRole("textbox", { name: "Message" });
    await userEvent.type(typed, "See you at nine.");

    await endSession(router);

    await expect.element(banner()).toBeVisible();
    await expect.element(typed).toHaveValue("See you at nine.");
    expect(router.state.location.pathname).toBe(`/mail/inbox/${row.id}`);
    // The list's refresh failed on the same 401: inbok answered, so the
    // list doesn't say it isn't answering.
    await new Promise((done) => setTimeout(done, 1000));
    expect(page.getByText("Couldn't refresh.").query()).toBeNull();

    // A press says why it failed, at the button, as any failure does.
    const reply = page.getByRole("form", { name: "Reply" });
    await userEvent.click(reply.getByRole("button", { name: "Send" }));
    await expect.element(reply.getByRole("alert")).toHaveTextContent("It failed.");
    await expect.element(typed).toHaveValue("See you at nine.");

    await userEvent.click(page.getByRole("button", { name: "Discard" }));
    await press("Discard");

    await expect.element(page.getByText("Your session ended. Sign in to continue.")).toBeVisible();
    expect(router.state.location.search).toEqual({ redirect: `/mail/inbox/${row.id}` });
  });

  it("keeps the compose window's message on a phone, saying so in the window", async () => {
    const row = conversation(1);
    const router = await open("/compose", row, 390);
    const window = page.getByRole("dialog");
    await userEvent.type(window.getByRole("textbox", { name: "Subject" }), "Lunch");

    await endSession(router);

    // In the window, which covers the frame's place for it; only one shows.
    await expect.element(window.getByRole("region", { name: "Signed out" })).toBeVisible();
    expect(banner().elements()).toHaveLength(1);

    // Leaving for sign-in would close the window, so it asks.
    await userEvent.click(window.getByRole("link", { name: "sign in" }));
    await press("Discard");

    await expect.element(page.getByText("Your session ended. Sign in to continue.")).toBeVisible();
    expect(router.state.location.search).toEqual({ redirect: "/mail/inbox" });
  });

  it("rolls back a star on a kept page and says why", async () => {
    const row = conversation(1);
    const router = await open("/compose", row);
    await userEvent.type(page.getByRole("textbox", { name: "Subject" }), "Lunch");
    await endSession(router);
    await expect.element(banner()).toBeVisible();

    const listed = page.getByRole("row", { name: /Subject 1,/ });
    await userEvent.hover(listed);
    await userEvent.click(listed.getByRole("button", { name: /star/i }));

    await expect.element(page.getByText(/Couldn't star/)).toBeVisible();
    await expect.element(page.getByRole("textbox", { name: "Subject" })).toHaveValue("Lunch");
  });

  it("says a page can't load while the compose window keeps its message", async () => {
    const row = conversation(1);
    const router = await open("/compose", row);
    const window = page.getByRole("region", { name: "New message" });
    await userEvent.type(window.getByRole("textbox", { name: "Subject" }), "Lunch");
    await endSession(router);
    await expect.element(banner()).toBeVisible();

    // A conversation the list doesn't hold, which its loader must read.
    await router.navigate({
      to: "/mail/$place/$conversationId",
      params: { place: "inbox", conversationId: "00000000-0000-4000-8000-00000000ffff" },
    });

    await expect.element(page.getByRole("heading", { name: "You're signed out" })).toBeVisible();
    await expect.element(page.getByRole("textbox", { name: "Subject" })).toHaveValue("Lunch");
  });

  it("opens sign-in without the message for a visitor who had no session", async () => {
    // The guard's own session read gets the 401, before any page draws.
    const router = await appRouter("/mail/inbox");

    expect(router.state.location.pathname).toBe("/sign-in");
    expect(router.state.location.search).toEqual({ redirect: "/mail/inbox" });
    expect(router.state.location.state.sessionEnded).toBeUndefined();
  });

  it("opens sign-in from Security, which says why and returns there", async () => {
    const server = fakeServer(memberSession);
    current = await appRouter("/settings/security", server);
    await render(<RouterProvider router={current} />);
    await expect.element(page.getByRole("heading", { level: 1, name: "Security" })).toBeVisible();

    server.session = undefined;
    await current.options.context.queryClient.refetchQueries({ type: "active" });

    await expect.element(page.getByText("Your session ended. Sign in to continue.")).toBeVisible();
    expect(current.state.location.search).toEqual({ redirect: "/settings/security" });
  });

  it("opens sign-in from the Outbox, which says why and returns there", async () => {
    const row = conversation(1);
    const router = await open("/mail/outbox", row);
    await expect.element(page.getByRole("heading", { level: 1, name: "Outbox" })).toBeVisible();

    api.signedOut = true;
    await router.options.context.queryClient.refetchQueries({ type: "active" });

    await expect.element(page.getByText("Your session ended. Sign in to continue.")).toBeVisible();
    expect(router.state.location.search).toEqual({ redirect: "/mail/outbox" });
  });

  it("is not what a Sign out says, even when the session was already gone", async () => {
    const row = conversation(1);
    const router = await open("/mail/inbox", row);
    await userEvent.click(page.getByRole("button", { name: /ana@example.com/ }));
    api.signedOut = true;

    await userEvent.click(page.getByRole("button", { name: "Sign out" }));

    await expect.poll(() => router.state.location.pathname).toBe("/sign-in");
    await expect.element(page.getByRole("heading", { name: "Sign in to inbok" })).toBeVisible();
    expect(page.getByText("Your session ended. Sign in to continue.").query()).toBeNull();
  });
});
