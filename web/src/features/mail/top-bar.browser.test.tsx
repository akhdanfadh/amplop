import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { cleanup, render } from "vitest-browser-react";
import { Frame } from "@/components/frame";
import { clearAccountCache } from "@/features/auth/session-cache";
import { conversation, listedAccount, mailApi } from "@/test/mail-api";
import { PlacePage, type PlaceAccount } from "./place-page";

const projects = "22222222-2222-4222-8222-222222222222";

let current: QueryClient | undefined;

// The place a test opens, as the place route gives it to its page: its
// name, its own name, the account it shows, and whether a conversation is
// open.
type Opened = {
  place?: string;
  name?: string;
  ownName?: string;
  account?: PlaceAccount;
  pane?: "list" | "reader";
};

// open renders the signed-in frame around a place's page, on a window
// width wide, with two accounts.
//
// DESIGN: not the whole app (test/app-router.ts): rendered at a drawer's
// width after other tests, it froze Chromium (DEBT.md, "Find why a
// whole-app browser test freezes on some presses").
async function open(
  width: number,
  { place = "inbox", name = "Inbox", ownName = name, account, pane = "list" }: Opened = {},
) {
  await page.viewport(width, 800);
  const api = mailApi(
    [conversation(1)],
    [listedAccount(1, "ana@example.com"), listedAccount(2, "ana@work.example")],
  );
  api.places = [
    { accountId: 1, place: "inbox", name: null, parent: null, unread: 3 },
    { accountId: 2, place: "inbox", name: null, parent: null, unread: 2 },
  ];
  vi.stubGlobal("fetch", vi.fn(api.fetch));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  current = client;
  const root = createRootRoute({
    component: () => (
      <QueryClientProvider client={client}>
        <Frame label="Mail" nav={null}>
          <Outlet />
        </Frame>
      </QueryClientProvider>
    ),
  });
  const mail = createRoute({
    getParentRoute: () => root,
    path: "/mail/$place",
    component: () => (
      <div className="flex h-full flex-col">
        <PlacePage
          place={place}
          name={name}
          ownName={ownName}
          account={account}
          openId={undefined}
          pane={pane}
          reader={null}
        />
      </div>
    ),
  });
  const router = createRouter({
    routeTree: root.addChildren([mail]),
    history: createMemoryHistory({ initialEntries: [`/mail/${place}`] }),
  });
  await render(<RouterProvider router={router} />);
}

afterEach(async () => {
  await cleanup();
  if (current) clearAccountCache(current);
  current = undefined;
  vi.unstubAllGlobals();
});

describe("the top bar, where the sidebar is a drawer", () => {
  it("holds the place's heading on a phone, without the sidebar's counts", async () => {
    await open(390);

    const bar = page.getByRole("banner");
    await expect.element(bar.getByRole("heading", { level: 1, name: "Inbox" })).toBeVisible();
    // The list's bold rows show what is unread; the drawer has the counts.
    expect(bar.getByText(/unread/).elements()).toHaveLength(0);
    // The wordmark gives way to the place's own bar.
    expect(bar.getByRole("link", { name: "inbok" }).elements()).toHaveLength(0);
  });

  it("holds them from 780 px to 1023 px too", async () => {
    await open(900);

    const bar = page.getByRole("banner");
    await expect.element(bar.getByRole("heading", { level: 1, name: "Inbox" })).toBeVisible();
  });

  it("shows a nested label by its own name, its path read to screen readers", async () => {
    await open(390, {
      place: projects,
      name: "Work/Projects",
      ownName: "Projects",
      account: { id: 1, color: 1, email: "ana@example.com", name: null },
    });

    const bar = page.getByRole("banner");
    await expect
      .element(bar.getByRole("heading", { level: 1, name: "Work/Projects" }))
      .toBeInTheDocument();
    await expect.element(bar.getByText("Projects", { exact: true })).toBeVisible();
    await expect.element(bar.getByText("ana@example.com")).toBeVisible();
  });

  it("leaves the bar to the wordmark while a conversation is open on a phone", async () => {
    await open(390, { pane: "reader" });

    const bar = page.getByRole("banner");
    await expect.element(bar.getByRole("link", { name: "inbok" })).toBeVisible();
    expect(bar.getByRole("heading", { level: 1, name: "Inbox" }).elements()).toHaveLength(0);
  });
});
