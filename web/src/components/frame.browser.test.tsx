import type { ReactNode } from "react";
import { describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import type { CurrentSession } from "@/api/generated/model";
import { ComposeProvider, ComposeStore } from "@/features/outgoing-messages/compose-store";
import { ComposeWindow } from "@/features/outgoing-messages/compose-window";
import { pressInDialog } from "@/test/dialog";
import { type Server, fakeServer, memberSession } from "@/test/server";
import { stubRouter } from "@/test/stub-router";
import { Frame } from "./frame";
import { LeaveWarning } from "./leave-warning";

const noContent = 204;

// The member the frame names.
const ada: CurrentSession = { ...memberSession, email: "ada@example.org" };

// serve installs a server ada is signed in to, where Sign Out ends her
// session, and returns it.
function serve(): Server {
  const server = fakeServer(ada).install();
  server.on("DELETE", "/api/v0/sessions/current", () => {
    server.session = undefined;
    return new Response(null, { status: noContent });
  });
  return server;
}

// Inbox is a page holding typed text, which a leave warning guards.
function Inbox() {
  return (
    <>
      <h1>Inbox</h1>
      <LeaveWarning
        active
        onSessionEnd="stay"
        text={{
          title: "Leave without sending?",
          description: "Your message will be lost.",
          stay: "Keep editing",
          leave: "Leave",
          leaveVariant: "destructive",
        }}
      />
    </>
  );
}

// Shown says what show renders: compose is the compose window's store, a
// new one when left out; inbox is the page in the frame, Inbox when left
// out; beside goes next to the frame, e.g. the compose window.
type Shown = { compose?: ComposeStore; inbox?: ReactNode; beside?: ReactNode };

// show renders the frame named Mail, with a link in its nav, around the
// page, with a blank /sign-in page to leave for, and returns the router.
async function show({ compose = new ComposeStore(), inbox = <Inbox />, beside }: Shown = {}) {
  const { router } = await stubRouter(inbox, {
    pages: ["/sign-in"],
    layout: (shown) => (
      <ComposeProvider value={compose}>
        <Frame label="Mail" nav={<a href="#inbox">Inbox</a>}>
          {shown}
        </Frame>
        {beside}
      </ComposeProvider>
    ),
  });
  return router;
}

// signOut presses the member menu's Sign out.
async function signOut(): Promise<void> {
  await userEvent.click(page.getByRole("button", { name: /ada@example.org/ }));
  await userEvent.click(page.getByRole("button", { name: "Sign out" }));
}

describe("Frame", () => {
  it("gives focus to the sidebar's button when the window outgrows the open drawer", async () => {
    serve();
    await page.viewport(900, 800);
    await show();
    await userEvent.click(page.getByRole("button", { name: "Open sidebar" }));
    const drawer = page.getByRole("dialog", { name: "Mail" });
    await expect.element(drawer).toBeVisible();
    // Focus is in the drawer, for the wider window to take away.
    await expect.poll(() => drawer.element().contains(document.activeElement)).toBe(true);

    await page.viewport(1440, 900);
    await expect.element(drawer).not.toBeInTheDocument();
    await expect
      .element(
        page
          .getByRole("navigation", { name: "Mail" })
          .getByRole("button", { name: "Collapse sidebar" }),
      )
      .toHaveFocus();
  });

  it("opens the drawer with its button where the top bar's was, so nothing jumps", async () => {
    serve();
    await show();
    for (const width of [390, 900]) {
      // oxlint-disable-next-line eslint/no-await-in-loop -- one window at a time.
      await page.viewport(width, 800);
      const open = page.getByRole("banner").getByRole("button", { name: "Open sidebar" });
      // oxlint-disable-next-line eslint/no-await-in-loop -- one window at a time.
      await expect.element(open).toBeVisible();
      // The top bar's button stays where it is under the open drawer, out
      // of a locator's reach.
      const openButton = open.element();
      // oxlint-disable-next-line eslint/no-await-in-loop -- one window at a time.
      await userEvent.click(open);
      const close = page.getByRole("dialog").getByRole("button", { name: "Close sidebar" });
      // oxlint-disable-next-line eslint/no-await-in-loop -- one window at a time.
      await expect.element(close).toBeVisible();
      // Once the drawer has slid in, its button is no pixel right of or
      // below the top bar's.
      // oxlint-disable-next-line eslint/no-await-in-loop -- one window at a time.
      await expect
        .poll(() => {
          const before = openButton.getBoundingClientRect();
          const after = close.element().getBoundingClientRect();
          return { width, right: after.x - before.x, down: after.y - before.y };
        })
        .toEqual({ width, right: 0, down: 0 });
      // By its button, not Escape: the pointer rests on that button, and
      // while its tooltip shows, Escape closes only the tooltip.
      // oxlint-disable-next-line eslint/no-await-in-loop -- one window at a time.
      await userEvent.click(close);
      // oxlint-disable-next-line eslint/no-await-in-loop -- one window at a time.
      await expect.element(page.getByRole("dialog")).not.toBeInTheDocument();
    }
  });

  it("names the member in a tooltip on the rail, whose button opens the member menu", async () => {
    serve();
    localStorage.setItem("inbok-sidebar-open", "false");
    await show();
    const member = page.getByRole("button", { name: "Member menu, ada@example.org" });
    // React Aria shows tooltips on hover once the pointer is in use.
    await userEvent.click(page.getByRole("heading", { name: "Inbox" }));
    await userEvent.hover(member);
    await expect.element(page.getByRole("tooltip")).toHaveTextContent("ada@example.org");

    await userEvent.click(member);
    await expect.element(page.getByRole("dialog", { name: "Member menu" })).toBeVisible();
  });

  it("asks nothing for a step that keeps the page's address", async () => {
    serve();
    const router = await show();
    await expect.element(page.getByRole("heading", { name: "Inbox" })).toBeVisible();

    // As the compose window's entry on a phone: the same address, new state.
    router.history.push(router.history.location.href, { composeWindow: "one" });
    await expect.poll(() => router.history.location.state.composeWindow).toBe("one");
    expect(page.getByRole("alertdialog").query()).toBeNull();
  });

  it("signs out to sign-in past the page's leave warning, so nothing stays on screen", async () => {
    serve();
    const router = await show();
    await userEvent.click(page.getByRole("button", { name: /ada@example.org/ }));
    await userEvent.click(page.getByRole("button", { name: "Sign out" }));

    await expect.element(page.getByRole("heading", { name: "/sign-in" })).toBeVisible();
    expect(router.state.location.pathname).toBe("/sign-in");
    expect(page.getByRole("alertdialog").query()).toBeNull();
  });

  it("asks before signing out while the compose window holds a message", async () => {
    const server = serve();
    const compose = new ComposeStore();
    compose.open();
    compose.setLoses("typed");
    await show({ compose });
    await signOut();
    await pressInDialog("Keep writing");
    expect(server.requests.some((asked) => asked.method === "DELETE")).toBe(false);

    await signOut();
    await pressInDialog("Sign out");
    await expect.element(page.getByRole("heading", { name: "/sign-in" })).toBeVisible();
  });

  it("says what signing out while sending means, without claiming it wasn't sent", async () => {
    serve();
    const compose = new ComposeStore();
    compose.open();
    compose.setLoses("sending");
    await show({ compose });

    await signOut();
    await expect
      .element(page.getByRole("alertdialog", { name: "Sign out while sending?" }))
      .toMatchTextContent("check the Outbox");
  });

  it("lets F6 move between the page and the compose window", async () => {
    serve();
    const compose = new ComposeStore();
    await show({
      compose,
      inbox: (
        <>
          <h1 tabIndex={-1}>Inbox</h1>
          <button type="button">Archive</button>
        </>
      ),
      beside: <ComposeWindow />,
    });
    await userEvent.click(page.getByRole("button", { name: "Archive" }));
    compose.open();
    const window = page.getByRole("region", { name: "New message" });
    await expect.element(window).toBeVisible();
    await userEvent.click(page.getByRole("button", { name: "Archive" }));

    await userEvent.keyboard("{F6}");
    await expect.poll(() => window.element().contains(document.activeElement)).toBe(true);
    await userEvent.keyboard("{F6}");
    await expect.element(page.getByRole("button", { name: "Archive" })).toHaveFocus();
  });
});
