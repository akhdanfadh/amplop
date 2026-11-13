import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  RouterProvider,
  createMemoryHistory,
  createRootRouteWithContext,
  createRoute,
  createRouter,
  useRouterState,
} from "@tanstack/react-router";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { cleanup, render } from "vitest-browser-react";
import { useListConnectedAccounts } from "@/api/generated/inbok";
import { clearAccountCache } from "@/features/auth/session-cache";
import { Route as appRoot } from "@/routes/__root";
import { conversation, listedAccount, mailApi, type MailApi } from "@/test/mail-api";
import { placeSearch } from "./address";
import { refreshNewest } from "./conversations";
import { PlacePage } from "./place-page";

const serverError = 500;

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

// app renders the Inbox's page on api at address, filtered to the account
// its ?account= names as the app's route does, with openId open, or else
// the conversation the address opens, and returns its query client and its
// router.
async function app(api: MailApi, address = "/mail/inbox", openId?: string) {
  vi.stubGlobal("fetch", vi.fn(api.fetch));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  current = client;
  const root = createRootRouteWithContext<{ queryClient: QueryClient }>()({
    component: appRoot.options.component,
  });
  const place = createRoute({
    getParentRoute: () => root,
    path: "/mail/$place",
    validateSearch: placeSearch,
    component: function Place() {
      const { place: name } = place.useParams();
      const { account: id } = place.useSearch();
      const account = useListConnectedAccounts().data?.data.accounts.find((a) => a.id === id);
      // /mail/<place>/<conversation id>: the conversation the address opens.
      const routed = useRouterState({
        select: (state) => state.location.pathname.split("/")[3],
      });
      // The window's height, as the mail frame gives its panes.
      return (
        <div className="flex h-svh flex-col">
          <PlacePage
            place={name}
            name={name === "inbox" ? "Inbox" : name}
            account={account}
            openId={openId ?? routed}
            pane="list"
            reader={null}
          />
        </div>
      );
    },
  });
  // A conversation's address, so opening one stays on the place's page.
  const opened = createRoute({ getParentRoute: () => place, path: "$conversationId" });
  const signIn = createRoute({ getParentRoute: () => root, path: "/sign-in" });
  const router = createRouter({
    routeTree: root.addChildren([place.addChildren([opened]), signIn]),
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

  it("keeps new mail behind a button while the member is scrolled down", async () => {
    const api = mailApi(
      Array.from({ length: 60 }, (_, n) => conversation(n)),
      one,
    );
    const { client } = await app(api);
    await expect.element(page.getByRole("row", { name: /Subject 59,/ })).toBeVisible();
    const last = page.getByRole("row", { name: /Subject 10,/ });
    // End again until it lands: one pressed as the list settles can go
    // nowhere. The first page's last row is the last until it does.
    await vi.waitFor(
      async () => {
        await toLastRow();
        await expect.element(last).toHaveFocus();
      },
      { timeout: 10_000 },
    );
    api.conversations.push(conversation(100), conversation(101));
    await refreshNewest(client);

    const button = page.getByRole("button", { name: "2 new conversations" });
    await expect.element(button).toBeVisible();
    // The list stays where it was: the new ones wait above, undrawn.
    await expect.element(last).toBeVisible();
    expect(page.getByRole("row", { name: /Subject 101,/ }).query()).toBeNull();

    await userEvent.click(button);
    await expect.element(page.getByRole("row", { name: /Subject 101,/ })).toBeVisible();
    await expect.element(button).not.toBeInTheDocument();
  });

  it("shows new mail at once at the top of the list", async () => {
    const api = mailApi([conversation(1)], one);
    const { client } = await app(api);
    await expect.element(page.getByRole("row", { name: /Subject 1,/ })).toBeVisible();
    api.conversations.push(conversation(2));
    await refreshNewest(client);
    await expect.element(page.getByRole("row", { name: /Subject 2,/ })).toBeVisible();
    expect(page.getByRole("button", { name: /new conversation/ }).query()).toBeNull();
  });

  it("asks for a first account when there is none", async () => {
    await app(mailApi([], []));
    await expect
      .element(page.getByRole("heading", { name: "Connect your first account", level: 2 }))
      .toBeVisible();
    await expect.element(page.getByRole("link", { name: "Connect Gmail" })).toBeVisible();
  });

  it("says a place is empty, and that mail is still being copied", async () => {
    const syncing = listedAccount(1, "ana@example.com");
    syncing.sync.status = "running";
    await app(mailApi([], [syncing]));
    await expect.element(page.getByRole("heading", { name: "Nothing in Inbox" })).toBeVisible();
    await expect
      .element(page.getByText("Some accounts are still syncing.", { exact: false }))
      .toBeVisible();
  });

  it("names Spam's own empty state", async () => {
    await app(mailApi([conversation(1)], one), "/mail/spam");
    await expect.element(page.getByRole("heading", { name: "No spam" })).toBeVisible();
  });

  it("offers Try again when the first page fails", async () => {
    const api = mailApi([conversation(1)], one);
    api.failNext = serverError;
    await app(api);
    await expect.element(page.getByText("Couldn't load these conversations.")).toBeVisible();
    await userEvent.click(page.getByRole("button", { name: "Try again" }));
    await expect.element(page.getByRole("row", { name: /Subject 1,/ })).toBeVisible();
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

  it("adds an opened conversation to the history, and replaces it with the next", async () => {
    const { router } = await app(mailApi([conversation(1), conversation(2), conversation(3)], one));
    const before = router.history.length;

    await userEvent.click(page.getByRole("row", { name: /Subject 1,/ }));
    await expect
      .poll(() => router.state.location.pathname)
      .toBe(`/mail/inbox/${conversation(1).id}`);
    expect(router.history.length).toBe(before + 1);
    // With one open, each next takes its place.
    await userEvent.click(page.getByRole("row", { name: /Subject 2,/ }));
    await expect
      .poll(() => router.state.location.pathname)
      .toBe(`/mail/inbox/${conversation(2).id}`);
    await userEvent.click(page.getByRole("row", { name: /Subject 3,/ }));
    await expect
      .poll(() => router.state.location.pathname)
      .toBe(`/mail/inbox/${conversation(3).id}`);
    expect(router.history.length).toBe(before + 1);
    router.history.back();
    await expect.poll(() => router.state.location.pathname).toBe("/mail/inbox");
  });

  it("marks the conversation the reading pane shows", async () => {
    await app(mailApi([conversation(1), conversation(2)], one), "/mail/inbox", conversation(1).id);
    await expect
      .element(page.getByRole("row", { name: /Subject 1,/ }))
      .toHaveAttribute("aria-current", "page");
    await expect
      .element(page.getByRole("row", { name: /Subject 2,/ }))
      .not.toHaveAttribute("aria-current");
  });
});

describe("one account's list", () => {
  const two = [listedAccount(1, "ana@example.com"), listedAccount(2, "ana@work.example", 3)];

  it("shows only that account's mail, without rings", async () => {
    await app(
      mailApi([conversation(1), conversation(2, { accountId: 2 })], two),
      "/mail/inbox?account=2",
    );
    const row = page.getByRole("row", { name: /Subject 2,/ });
    await expect.element(row).toBeVisible();
    expect(page.getByRole("row", { name: /Subject 1,/ }).query()).toBeNull();
    // The heading names the account, so its rows don't.
    expect(row.element().getAttribute("aria-label")).not.toContain("ana@work.example");
    const avatar = row.element().querySelector('[data-slot="avatar"]');
    expect(avatar?.className).not.toContain("after:border-account");
  });

  it("names the account when its list is empty or still syncing", async () => {
    const syncing = listedAccount(2, "ana@work.example", 3);
    syncing.sync.status = "running";
    await app(mailApi([conversation(1)], [two[0], syncing]), "/mail/inbox?account=2");
    await expect
      .element(page.getByRole("heading", { name: "Nothing in ana@work.example's Inbox" }))
      .toBeVisible();
    await expect
      .element(page.getByText("ana@work.example is still syncing.", { exact: false }))
      .toBeVisible();
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

describe("the list's failures", () => {
  it("keeps its rows and stops when an older page fails, then tries again", async () => {
    const api = mailApi(
      Array.from({ length: 300 }, (_, n) => conversation(n)),
      one,
    );
    await app(api);
    await expect.element(page.getByRole("row", { name: /Subject 299,/ })).toBeVisible();
    api.failOlder = serverError;
    const before = api.requests.length;
    await toLastRow();
    const failed = page
      .getByRole("alert")
      .filter({ hasText: "Couldn't load older conversations." });
    await expect.element(failed).toBeVisible();
    await new Promise((resolve) => setTimeout(resolve, 1500));
    // No storm: the failed page, not a read of the whole place.
    const reads = api.requests.slice(before).filter((r) => r.startsWith("/api/v0/conversations?"));
    expect(reads.length).toBeLessThanOrEqual(2);
    expect(reads.every((r) => r.includes("before="))).toBe(true);
    // The rows already loaded stay, down to the last one.
    await expect.element(page.getByRole("row", { name: /Subject 250,/ })).toBeVisible();

    api.failOlder = undefined;
    await userEvent.click(page.getByRole("button", { name: "Try again" }));
    await expect.element(failed).not.toBeInTheDocument();
    await expect.element(page.getByRole("row", { name: /Subject 299,/ })).toBeVisible();
  });

  it("opens sign-in, its mail gone, when the session has ended", async () => {
    const api = mailApi([conversation(1)], one);
    api.signedOut = true;
    const { router } = await app(api);
    await vi.waitFor(() => expect(router.state.location.pathname).toBe("/sign-in"), {
      timeout: 10_000,
    });
    const reads = api.requests.filter((r) => r.startsWith("/api/v0/conversations?"));
    // One read, not a loop of them once the mail is dropped.
    expect(reads.length).toBeLessThanOrEqual(2);
  });

  it("keeps its rows and says since when after a refresh fails", async () => {
    const api = mailApi([conversation(1)], one);
    const { client } = await app(api);
    const row = page.getByRole("row", { name: /Subject 1,/ });
    await expect.element(row).toBeVisible();
    api.failNext = serverError;
    await refreshNewest(client);
    await expect
      .element(page.getByRole("status").filter({ hasText: "Couldn't refresh." }))
      .toBeVisible();
    await expect.element(row).toBeVisible();
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
    // 70 key presses, each waiting for its row: slow on a busy machine.
  }, 60_000);

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

  it("keeps the first row in place when one above it moves to the top", async () => {
    const api = mailApi(
      Array.from({ length: 60 }, (_, n) => conversation(n)),
      one,
    );
    const { client } = await app(api);
    await expect.element(page.getByRole("row", { name: /Subject 59,/ })).toBeVisible();
    await toLastRow();
    const box = grid().element();
    const top = box.getBoundingClientRect().top + 4;
    const atTop = () =>
      document
        .elementFromPoint(box.getBoundingClientRect().left + 40, top)
        ?.closest("[role=row]")
        ?.getAttribute("aria-label");
    const shown = atTop();
    // A reply to Subject 40, above the screen: it becomes the newest.
    const replied = api.conversations.find((c) => c.subject === "Subject 40");
    if (replied) replied.date = new Date(Date.UTC(2026, 10, 13, 9, 30)).toISOString();
    await refreshNewest(client);
    await vi.waitFor(
      () => expect(page.getByRole("button", { name: /new conversation/ }).query()).not.toBeNull(),
      { timeout: 10_000 },
    );
    expect(atTop()).toBe(shown);
  });

  it("puts focus on the newest row once the new conversations show", async () => {
    const api = mailApi(
      Array.from({ length: 60 }, (_, n) => conversation(n)),
      one,
    );
    const { client } = await app(api);
    await expect.element(page.getByRole("row", { name: /Subject 59,/ })).toBeVisible();
    await toLastRow();
    api.conversations.push(conversation(100));
    await refreshNewest(client);
    const button = page.getByRole("button", { name: "1 new conversation" });
    await expect.element(button).toBeVisible();
    button.element().focus();
    await userEvent.keyboard("{Enter}");
    await expect.element(page.getByRole("row", { name: /Subject 100,/ })).toHaveFocus();
  });
});
