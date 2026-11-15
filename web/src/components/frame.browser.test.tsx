import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router";
import { Frame } from "./frame";
import { LeaveWarning } from "./leave-warning";

const noContent = 204;

// signedIn answers the session read: a member is signed in.
function signedIn(): Response {
  return new Response(
    JSON.stringify({
      data: { email: "ada@example.org", emailVerified: true, role: "member", totpRequired: false },
    }),
    { headers: { "Content-Type": "application/json" } },
  );
}

// Inbox is a page holding typed text, which a leave warning guards.
function Inbox() {
  return (
    <>
      <h1>Inbox</h1>
      <LeaveWarning
        active
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

// app returns the frame named Mail, with a link in its nav, around Inbox,
// with a sign-in page to leave for.
function app() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const root = createRootRoute({
    component: () => (
      <QueryClientProvider client={client}>
        <Frame label="Mail" nav={<a href="#inbox">Inbox</a>}>
          <Outlet />
        </Frame>
      </QueryClientProvider>
    ),
  });
  const home = createRoute({ getParentRoute: () => root, path: "/", component: Inbox });
  const signIn = createRoute({
    getParentRoute: () => root,
    path: "/sign-in",
    component: () => <h1>Sign in</h1>,
  });
  return createRouter({
    routeTree: root.addChildren([home, signIn]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
}

// frame lets React render what a native event changed.
function frame(): Promise<void> {
  return new Promise((resolve) =>
    requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe("Frame", () => {
  it("gives focus to the sidebar's button when the window outgrows the open drawer", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn<typeof fetch>(async () => signedIn()),
    );
    await page.viewport(900, 800);
    await render(<RouterProvider router={app()} />);
    await userEvent.click(page.getByRole("button", { name: "Open sidebar" }));
    const drawer = page.getByRole("dialog", { name: "Mail" });
    await expect.element(drawer).toBeVisible();
    await frame();

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
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => signedIn()),
    );
    await render(<RouterProvider router={app()} />);
    for (const width of [390, 900]) {
      // oxlint-disable-next-line eslint/no-await-in-loop -- one window at a time.
      await page.viewport(width, 800);
      const open = page.getByRole("banner").getByRole("button", { name: "Open sidebar" });
      // oxlint-disable-next-line eslint/no-await-in-loop -- one window at a time.
      await expect.element(open).toBeVisible();
      const before = open.element().getBoundingClientRect();
      // oxlint-disable-next-line eslint/no-await-in-loop -- one window at a time.
      await userEvent.click(open);
      const drawer = page.getByRole("dialog").getByRole("button", { name: "Close sidebar" });
      // oxlint-disable-next-line eslint/no-await-in-loop -- one window at a time.
      await expect.element(drawer).toBeVisible();
      // Once the drawer has slid in.
      // oxlint-disable-next-line eslint/no-await-in-loop -- one window at a time.
      await expect
        .poll(() => {
          const after = drawer.element().getBoundingClientRect();
          return { width, x: after.x, y: after.y };
        })
        .toEqual({ width, x: before.x, y: before.y });
      // oxlint-disable-next-line eslint/no-await-in-loop -- one window at a time.
      await userEvent.keyboard("{Escape}");
    }
  });

  it("names the member in a tooltip on the rail, whose button opens the member menu", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn<typeof fetch>(async () => signedIn()),
    );
    await page.viewport(1440, 900);
    localStorage.setItem("inbok-sidebar-open", "false");
    await render(<RouterProvider router={app()} />);
    const member = page.getByRole("button", { name: "Member menu, ada@example.org" });
    // React Aria shows tooltips on hover once the pointer is in use.
    await userEvent.click(page.getByRole("heading", { name: "Inbox" }));
    await userEvent.hover(member);
    await expect.element(page.getByRole("tooltip")).toHaveTextContent("ada@example.org");

    await userEvent.click(member);
    await expect.element(page.getByRole("dialog", { name: "Member menu" })).toBeVisible();
  });

  it("signs out to sign-in past the page's leave warning, so nothing stays on screen", async () => {
    const fetch = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(
      async (_input, init) =>
        init?.method === "DELETE" ? new Response(null, { status: noContent }) : signedIn(),
    );
    vi.stubGlobal("fetch", fetch);
    await page.viewport(1440, 900);
    const router = app();
    await render(<RouterProvider router={router} />);
    await userEvent.click(page.getByRole("button", { name: /ada@example.org/ }));
    await userEvent.click(page.getByRole("button", { name: "Sign out" }));

    await expect.element(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
    expect(router.state.location.pathname).toBe("/sign-in");
    expect(page.getByRole("alertdialog").query()).toBeNull();
  });
});
