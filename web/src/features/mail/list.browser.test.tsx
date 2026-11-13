import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  RouterProvider,
  createMemoryHistory,
  createRootRouteWithContext,
  createRoute,
  createRouter,
} from "@tanstack/react-router";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { cleanup, render } from "vitest-browser-react";
import { clearAccountCache } from "@/features/auth/session-cache";
import { Route as appRoot } from "@/routes/__root";
import { conversation, listedAccount, mailApi, type MailApi } from "@/test/mail-api";
import { refreshNewest } from "./conversations";
import { PlacePage } from "./place-page";
import { isViewPlace } from "./places";

// The query client of the test running, whose mail clearAccountCache
// ends, refresh timer included.
let current: QueryClient | undefined;

afterEach(async () => {
  // Unmounted first, so no list starts reading again once its mail ends.
  await cleanup();
  if (current) clearAccountCache(current);
  current = undefined;
  vi.unstubAllGlobals();
});

// app renders the Inbox's page on api at address, and returns its query
// client and its router.
async function app(api: MailApi, address = "/mail/inbox") {
  vi.stubGlobal("fetch", vi.fn(api.fetch));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  current = client;
  const root = createRootRouteWithContext<{ queryClient: QueryClient }>()({
    component: appRoot.options.component,
  });
  const place = createRoute({
    getParentRoute: () => root,
    path: "/mail/$place",
    component: function Place() {
      const { place: name } = place.useParams();
      // The window's height, as the mail frame gives its panes.
      return (
        <div className="flex h-svh flex-col">
          <PlacePage place={isViewPlace(name) ? name : "inbox"} pane="list" reader={null} />
        </div>
      );
    },
  });
  const signIn = createRoute({ getParentRoute: () => root, path: "/sign-in" });
  const router = createRouter({
    routeTree: root.addChildren([place, signIn]),
    history: createMemoryHistory({ initialEntries: [address] }),
    context: { queryClient: client },
    Wrap: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
  });
  await page.viewport(1440, 900);
  await render(<RouterProvider router={router} />);
  return { client, router };
}

// grid returns the conversation list.
function grid() {
  return page.getByRole("grid", { name: "Conversations" });
}

// toLastRow moves keyboard focus to the list's last loaded row, which
// React Aria scrolls into view, as a member's End does; reaching it loads
// the next page.
async function toLastRow(): Promise<void> {
  const first = grid().element().querySelector('[role="row"][aria-label]');
  if (!grid().element().contains(document.activeElement) && first instanceof HTMLElement) {
    first.focus();
  }
  await userEvent.keyboard("{End}");
}

const one = [listedAccount(1, "ana@example.com")];

describe("ConversationList", () => {
  it("lists a place newest first and ends where the copied mail ends", async () => {
    const api = mailApi(
      Array.from({ length: 70 }, (_, n) => conversation(n)),
      one,
    );
    await app(api);
    await expect.element(page.getByRole("row", { name: /^Bo Tan, Subject 69,/ })).toBeVisible();
    const rows = grid().element().querySelectorAll('[role="row"][aria-label]');
    expect(rows[0]?.getAttribute("aria-label")).toMatch(/Subject 69/);
    expect(rows[1]?.getAttribute("aria-label")).toMatch(/Subject 68/);

    const end = page.getByText("That's everything inbok has copied.", { exact: false });
    // Each move to the last row loads the next page.
    await vi.waitFor(
      async () => {
        await toLastRow();
        expect(end.query()).not.toBeNull();
      },
      { timeout: 10_000 },
    );
    await expect.element(end).toBeVisible();
    // The oldest is the last row once everything is loaded.
    await toLastRow();
    await expect.element(page.getByRole("row", { name: /Subject 0,/ })).toHaveFocus();
    // Every read was a page: none asked for the whole place.
    const reads = api.requests.filter((r) => r.startsWith("/api/v0/conversations?"));
    expect(reads.every((r) => r.includes("limit="))).toBe(true);
    expect(reads.some((r) => r.includes("before="))).toBe(true);
  });

  it("shows new mail at once at the top of the list", async () => {
    const api = mailApi([conversation(1)], one);
    const { client } = await app(api);
    await expect.element(page.getByRole("row", { name: /Subject 1,/ })).toBeVisible();
    api.conversations.push(conversation(2));
    await refreshNewest(client);
    await expect.element(page.getByRole("row", { name: /Subject 2,/ })).toBeVisible();
  });

  it("names a starred conversation's row Starred", async () => {
    await app(mailApi([conversation(1, { starred: true })], one));
    await expect.element(page.getByRole("row", { name: /Subject 1, .*Starred/ })).toBeVisible();
  });

  it("lists several senders in order when the first one's name is right to left", async () => {
    const noor = { name: "نور الهدى", address: "noor@example.com" };
    const bo = { name: "Bo Tan", address: "bo@example.com" };
    await app(mailApi([conversation(1, { participants: [noor, bo] })], one));

    const row = page.getByRole("row", { name: /Subject 1,/ });
    await expect.element(row).toBeVisible();
    const names = [...row.element().querySelectorAll("bdi")];
    expect(names.map((name) => name.textContent)).toEqual(["نور", "Bo"]);
    // The line keeps the page's direction, so Bo stays after نور.
    expect(getComputedStyle(names[0].parentElement ?? names[0]).direction).toBe("ltr");
  });

  it("rings each row in its account's colour and names the account when accounts mix", async () => {
    const api = mailApi(
      [conversation(1), conversation(2, { accountId: 2 })],
      [listedAccount(1, "ana@example.com"), listedAccount(2, "ana@work.example", 3)],
    );
    await app(api);
    await expect
      .element(page.getByRole("row", { name: /Subject 2, .*ana@work\.example$/ }))
      .toBeVisible();
    const avatar = page
      .getByRole("row", { name: /Subject 2,/ })
      .element()
      .querySelector('[data-slot="avatar"]');
    expect(avatar?.className).toContain("after:border-account-3");
  });
});

describe("the list's rows", () => {
  it("follow a text size changed while the list is open", async () => {
    const api = mailApi([conversation(1), conversation(2)], one);
    await app(api);
    const row = page.getByRole("row", { name: /Subject 1,/ });
    await expect.element(row).toBeVisible();
    expect(row.element().getBoundingClientRect().height).toBe(76);
    // Twice the browser's usual 16 px, as a member's own text size.
    document.documentElement.style.fontSize = "32px";
    try {
      await vi.waitFor(() => expect(row.element().getBoundingClientRect().height).toBe(152), {
        timeout: 10_000,
      });
    } finally {
      document.documentElement.style.fontSize = "";
    }
  });
});

describe("the list's order and refreshes", () => {
  it("lists tied dates across a page's end in the server's order", async () => {
    // Groups of seven on one date, some with a fraction Go would write
    // shorter ("...:00.5Z"), so text order is not time order.
    const rows = Array.from({ length: 70 }, (_, n) =>
      conversation(n, {
        date: `2026-11-13T08:${String(Math.floor(n / 7)).padStart(2, "0")}:00${n % 7 === 0 ? ".5" : ""}Z`,
      }),
    );
    await app(mailApi(rows, one));
    await expect.element(grid()).toBeVisible();
    const expected = rows
      .map((row) => ({ subject: row.subject, date: Date.parse(row.date), id: row.id }))
      // oxlint-disable-next-line unicorn/no-array-sort -- a new array; tsconfig's lib has no toSorted.
      .sort((a, b) => b.date - a.date || (a.id < b.id ? 1 : -1))
      .map((row) => row.subject);
    const first = grid().element().querySelector('[role="row"][aria-label]');
    if (first instanceof HTMLElement) first.focus();
    const seen: string[] = [];
    for (const subject of expected) {
      // oxlint-disable-next-line eslint/no-await-in-loop -- one row at a time, as the member moves.
      await vi.waitFor(
        () => {
          const label = document.activeElement?.getAttribute("aria-label") ?? "";
          expect(label).toContain(`${subject},`);
        },
        { timeout: 10_000 },
      );
      seen.push(subject);
      // oxlint-disable-next-line eslint/no-await-in-loop -- one row at a time.
      await userEvent.keyboard("{ArrowDown}");
    }
    expect(seen).toEqual(expected);
  });

  it("refreshes only the newest page", async () => {
    const api = mailApi(
      Array.from({ length: 200 }, (_, n) => conversation(n)),
      one,
    );
    const { client } = await app(api);
    await expect.element(page.getByRole("row", { name: /Subject 199,/ })).toBeVisible();
    // Two older pages. End again until each is asked for: an End before a
    // page's rows are drawn stays on the last row and asks for nothing.
    const older = () => api.requests.filter((r) => r.includes("before=")).length;
    for (const pages of [1, 2]) {
      // oxlint-disable-next-line eslint/no-await-in-loop -- one page at a time.
      await vi.waitFor(
        async () => {
          await toLastRow();
          expect(older()).toBeGreaterThanOrEqual(pages);
        },
        { timeout: 10_000 },
      );
    }
    const before = api.requests.length;
    const paged = new Set(api.requests.filter((r) => r.includes("before=")));
    await refreshNewest(client);
    // The list may still be asking for a further page meanwhile; the
    // refresh reads the newest page and none of the older ones again.
    const reads = api.requests.slice(before).filter((r) => r.startsWith("/api/v0/conversations?"));
    expect(reads.filter((r) => !r.includes("before="))).toHaveLength(1);
    expect(reads.filter((r) => paged.has(r))).toEqual([]);
  });
});
