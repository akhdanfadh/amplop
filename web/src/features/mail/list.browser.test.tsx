import { QueryClient, QueryClientProvider, onlineManager } from "@tanstack/react-query";
import {
  RouterProvider,
  createMemoryHistory,
  createRootRouteWithContext,
  createRoute,
  createRouter,
  useRouterState,
} from "@tanstack/react-router";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { cdp, page, userEvent } from "vite-plus/test/browser/context";
import { cleanup, render } from "vitest-browser-react";
import { useListConnectedAccounts } from "@/api/generated/inbok";
import { clearAccountCache } from "@/features/auth/session-cache";
import { Route as appRoot } from "@/routes/__root";
import { conversation, listedAccount, mailApi, type MailApi } from "@/test/mail-api";
import { pointTo } from "@/test/pointer";
import { placeSearch } from "./address";
import { currentMail, refreshNewest } from "./conversations";
import { PlacePage } from "./place-page";
import { Toaster } from "@/components/ui/toast";

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
// the conversation the address opens, and returns its query client, its
// router, and the conversations the router read ahead.
async function app(api: MailApi, address = "/mail/inbox", openId?: string) {
  vi.stubGlobal("fetch", vi.fn(api.fetch));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  current = client;
  const readAhead: string[] = [];
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
  const opened = createRoute({
    getParentRoute: () => place,
    path: "$conversationId",
    loader: ({ params }) => {
      readAhead.push(params.conversationId);
    },
  });
  const signIn = createRoute({ getParentRoute: () => root, path: "/sign-in" });
  const router = createRouter({
    routeTree: root.addChildren([place.addChildren([opened]), signIn]),
    history: createMemoryHistory({ initialEntries: [address] }),
    context: { queryClient: client },
    Wrap: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
  });
  await page.viewport(1440, 900);
  await render(<RouterProvider router={router} />);
  return { client, router, readAhead };
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

// anchored waits until api receives the list's first request for how many
// conversations are newer, which the list sends once it has an anchor.
//
// DESIGN: focus can reach the last row before the list's scroll handler
// sets the anchor; mail refreshed in between would count as already shown.
async function anchored(api: MailApi): Promise<void> {
  await vi.waitFor(() =>
    expect(api.requests.some((r) => r.startsWith("/api/v0/conversations/new-count"))).toBe(true),
  );
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

  it("ends with its words 12 px under the last row", async () => {
    await app(mailApi([conversation(1)], one));
    const words = page.getByText("That's everything inbok has copied.", { exact: false });
    await expect.element(words).toBeVisible();

    const row = page
      .getByRole("row", { name: /Subject 1,/ })
      .element()
      .getBoundingClientRect();
    expect(words.element().getBoundingClientRect().top - row.bottom).toBe(12);
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
    await anchored(api);
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

  it("reads a conversation ahead when the pointer rests on its row", async () => {
    const { readAhead } = await app(mailApi([conversation(1)], one));
    const row = page.getByRole("row", { name: /Subject 1,/ });
    await expect.element(row).toBeVisible();
    pointTo(row.element());
    await vi.waitFor(() => expect(readAhead).toContain(conversation(1).id), { timeout: 10_000 });
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

// starOf returns the star button of the row named like name.
function starOf(name: RegExp) {
  return page.getByRole("row", { name }).getByRole("button", { name: "Star" });
}

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

  it("names the place when its list is empty, and the account still syncing", async () => {
    const syncing = listedAccount(2, "ana@work.example", 3);
    syncing.sync.status = "running";
    await app(mailApi([conversation(1)], [two[0], syncing]), "/mail/inbox?account=2");
    await expect.element(page.getByRole("heading", { name: "Nothing in Inbox" })).toBeVisible();
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

describe("the row's star", () => {
  it("shows on every row, and stars the conversation at Gmail", async () => {
    const api = mailApi([conversation(1), conversation(2, { starred: true })], one);
    await app(api);
    // At rest: the pointer off every row, wherever an earlier test left it.
    await userEvent.hover(page.getByRole("heading", { level: 1 }));
    const star = starOf(/^Bo Tan, Subject 1,/);
    await expect.element(star).toBeVisible();
    await expect.element(star).toHaveStyle({ opacity: "1" });
    await expect.element(starOf(/^Bo Tan, Subject 2,/)).toHaveStyle({ opacity: "1" });

    await userEvent.click(star);
    await expect.element(star).toHaveAttribute("aria-pressed", "true");
    expect(api.actions).toEqual([{ action: "star", ids: [conversation(1).id] }]);
    await expect.element(page.getByRole("row", { name: /Subject 1, .*Starred/ })).toBeVisible();
  });

  it("is reached from its row with the arrow keys, ← going straight to it", async () => {
    const api = mailApi([conversation(1)], one);
    await app(api);
    const row = page.getByRole("row", { name: /Subject 1,/ });
    await expect.element(row).toBeVisible();
    row.element().focus();
    await userEvent.keyboard("{ArrowLeft}");
    const star = starOf(/Subject 1,/);
    await expect.element(star).toHaveFocus();
    await userEvent.keyboard(" ");
    await expect.element(star).toHaveAttribute("aria-pressed", "true");
  });

  it("goes back and says why when Gmail refuses", async () => {
    const api = mailApi([conversation(1)], one);
    api.refuse[conversation(1).id] = "failed";
    await app(api);
    const row = page.getByRole("row", { name: /Subject 1,/ });
    await expect.element(row).toBeVisible();
    await userEvent.click(starOf(/Subject 1,/));
    await expect.element(page.getByText("Couldn't star the conversation")).toBeVisible();
    await expect.element(page.getByText("Gmail didn't answer. Try again.")).toBeVisible();
    await expect.element(starOf(/Subject 1,/)).toHaveAttribute("aria-pressed", "false");
  });

  it("removes a conversation Gmail no longer has, as the reading pane's actions do", async () => {
    const api = mailApi([conversation(2), conversation(1)], one);
    api.refuse[conversation(1).id] = "not-found";
    await app(api);
    await expect.element(page.getByRole("row", { name: /Subject 1,/ })).toBeVisible();
    // Gone at Gmail, and from inbok's lists once its poll noticed.
    api.conversations = [conversation(2)];

    await userEvent.click(starOf(/Subject 1,/));

    await expect.element(page.getByText("It's no longer in Gmail.")).toBeVisible();
    await expect.element(page.getByRole("row", { name: /Subject 1,/ })).not.toBeInTheDocument();
    expect(page.getByRole("button", { name: "Undo" }).query()).toBeNull();
  });

  it("goes back when the request fails", async () => {
    const api = mailApi([conversation(1, { starred: true })], one);
    api.failAction = serverError;
    await app(api);
    await userEvent.click(starOf(/Subject 1,/));
    await expect.element(page.getByText("Couldn't remove the star")).toBeVisible();
    await expect.element(starOf(/Subject 1,/)).toHaveAttribute("aria-pressed", "true");
  });
});

// readAndUnread returns an API listing an unread conversation, 1, above a
// read one, 2.
function readAndUnread() {
  return mailApi([conversation(1, { unread: true }), conversation(2)], one);
}

// subjectStyle returns the computed style of conversation n's subject text.
function subjectStyle(n: number) {
  return getComputedStyle(page.getByText(`Subject ${n}`, { exact: true }).element());
}

// bar returns the computed style of row's edge mark.
function bar(row: Element) {
  return getComputedStyle(row, "::before");
}

// The light theme's hover and open row fills (styles.css).
const accent = "rgb(236, 236, 236)";
const selection = "rgb(229, 234, 246)";

// rowActions returns the hovered-row actions of the row named like name.
function rowActions(name: RegExp): HTMLElement {
  const row = page.getByRole("row", { name }).element();
  const actions = row.querySelector<HTMLElement>("[data-slot=row-actions]");
  if (!actions) throw new Error("the row has no actions");
  return actions;
}

describe("a hovered row's actions", () => {
  it("show over the date only while the row is hovered, on the row's own fill", async () => {
    await app(mailApi([conversation(1), conversation(2)], one));
    const row = page.getByRole("row", { name: /Subject 1,/ });
    await expect.element(row).toBeVisible();
    // At rest: the pointer off every row, wherever an earlier test left it.
    await userEvent.hover(page.getByRole("heading", { level: 1 }));
    expect(getComputedStyle(rowActions(/Subject 1,/)).opacity).toBe("0");
    expect(getComputedStyle(rowActions(/Subject 1,/)).pointerEvents).toBe("none");

    await userEvent.hover(row);
    await expect.element(row).toHaveAttribute("data-hovered", "true");
    expect(getComputedStyle(rowActions(/Subject 1,/)).opacity).toBe("1");
    expect(getComputedStyle(rowActions(/Subject 2,/)).opacity).toBe("0");
    // The toolbar's, but the star the row already has, and Report spam.
    const names = [...row.element().querySelectorAll("button")].map((b) => b.ariaLabel);
    expect(names).toEqual(["Archive", "Delete", "Mark unread", "Star"]);
    await expect
      .poll(() => [
        getComputedStyle(rowActions(/Subject 1,/)).backgroundColor,
        getComputedStyle(row.element()).backgroundColor,
      ])
      .toEqual([accent, accent]);
  });

  it("are on the open row's fill there", async () => {
    await app(mailApi([conversation(1)], one), "/mail/inbox", conversation(1).id);
    const row = page.getByRole("row", { name: /Subject 1,/ });
    await userEvent.unhover(row);
    await userEvent.hover(row);
    await expect.element(row).toHaveAttribute("data-hovered", "true");
    await expect
      .poll(() => [
        getComputedStyle(rowActions(/Subject 1,/)).backgroundColor,
        getComputedStyle(row.element()).backgroundColor,
      ])
      .toEqual([selection, selection]);
  });

  it("are in the row's arrow-key path, showing while one has keyboard focus", async () => {
    await app(mailApi([conversation(1, { unread: true })], one));
    const row = page.getByRole("row", { name: /Subject 1,/ });
    await expect.element(row).toBeVisible();
    await userEvent.hover(page.getByRole("heading", { level: 1 }));
    row.element().focus();
    // Focus on the row keeps its date.
    expect(getComputedStyle(rowActions(/Subject 1,/)).opacity).toBe("0");
    await userEvent.keyboard("{ArrowRight}");
    await expect.element(row.getByRole("button", { name: "Archive" })).toHaveFocus();
    expect(getComputedStyle(rowActions(/Subject 1,/)).opacity).toBe("1");
    await userEvent.keyboard("{ArrowRight}");
    await expect.element(row.getByRole("button", { name: "Delete" })).toHaveFocus();
    await userEvent.keyboard("{ArrowRight}");
    await expect.element(row.getByRole("button", { name: "Mark read" })).toHaveFocus();
    await userEvent.keyboard("{ArrowRight}");
    await expect.element(starOf(/Subject 1,/)).toHaveFocus();
  });

  it("archive a row with an Undo, focus going to the row that took its place", async () => {
    const api = mailApi([conversation(1), conversation(2), conversation(3)], one);
    await app(api);
    const row = page.getByRole("row", { name: /Subject 2,/ });
    await userEvent.unhover(row);
    await userEvent.hover(row);
    await userEvent.click(row.getByRole("button", { name: "Archive" }));
    await expect.element(row).not.toBeInTheDocument();
    expect(api.actions).toEqual([{ action: "archive", ids: [conversation(2).id] }]);
    await expect.element(page.getByRole("button", { name: "Undo" })).toBeVisible();
    await expect.element(page.getByRole("row", { name: /Subject 1,/ })).toHaveFocus();
  });

  it("take the open conversation's row back to the list, as the toolbar does", async () => {
    const api = mailApi([conversation(1), conversation(2)], one);
    const { router } = await app(api, `/mail/inbox/${conversation(2).id}`);
    const row = page.getByRole("row", { name: /Subject 2,/ });
    await userEvent.unhover(row);
    await userEvent.hover(row);
    await userEvent.click(row.getByRole("button", { name: "Archive" }));
    await expect.poll(() => router.state.location.pathname).toBe("/mail/inbox");
    await expect.element(page.getByRole("row", { name: /Subject 1,/ })).toHaveFocus();
  });

  it("take the open row back to the list after Mark unread, focus staying on it", async () => {
    const api = mailApi([conversation(1), conversation(2)], one);
    const { router } = await app(api, `/mail/inbox/${conversation(2).id}`);
    const row = page.getByRole("row", { name: /Subject 2,/ });
    await userEvent.unhover(row);
    await userEvent.hover(row);
    await userEvent.click(row.getByRole("button", { name: "Mark unread" }));
    await expect.poll(() => router.state.location.pathname).toBe("/mail/inbox");
    expect(api.actions).toEqual([{ action: "unread", ids: [conversation(2).id] }]);
    await expect.element(page.getByRole("row", { name: /Subject 2,/ })).toHaveFocus();
  });

  it("know a row opened after the list drew it as the open one", async () => {
    const api = mailApi([conversation(1), conversation(2)], one);
    const { router } = await app(api);
    const row = page.getByRole("row", { name: /Subject 2,/ });
    // React Aria keeps a drawn row, and the callbacks it was drawn with.
    await userEvent.click(row);
    await expect
      .poll(() => router.state.location.pathname)
      .toBe(`/mail/inbox/${conversation(2).id}`);
    await new Promise((done) => setTimeout(done, 500));
    await userEvent.unhover(row);
    await userEvent.hover(row);
    await userEvent.click(row.getByRole("button", { name: "Mark unread" }));
    await expect.poll(() => router.state.location.pathname).toBe("/mail/inbox");
    expect(api.actions).toEqual([{ action: "unread", ids: [conversation(2).id] }]);
  });

  it("keep a mark on a row that is not open in place, its button keeping focus", async () => {
    const api = mailApi([conversation(1, { unread: true })], one);
    const { router } = await app(api);
    const row = page.getByRole("row", { name: /Subject 1,/ });
    row.element().focus();
    await userEvent.keyboard("{ArrowRight}{ArrowRight}{ArrowRight}");
    await userEvent.keyboard("{Enter}");
    // One slot, so focus stays as its action turns to Mark unread.
    await expect.element(row.getByRole("button", { name: "Mark unread" })).toHaveFocus();
    expect(api.actions).toEqual([{ action: "read", ids: [conversation(1).id] }]);
    expect(router.state.location.pathname).toBe("/mail/inbox");
  });

  it("drop a press in the list for a moment after one, so a double press acts once", async () => {
    const api = mailApi([conversation(1), conversation(2), conversation(3)], one);
    const { router } = await app(api);
    const row = page.getByRole("row", { name: /Subject 1,/ });
    await userEvent.unhover(row);
    await userEvent.hover(row);
    // The second press lands on Mark read, which took Mark unread's place.
    await userEvent.dblClick(row.getByRole("button", { name: "Mark unread" }));
    await expect.element(row.getByRole("button", { name: "Mark read" })).toBeInTheDocument();
    await new Promise((done) => setTimeout(done, 500));
    // Archive, then a press on the row that slid under the pointer.
    const second = page.getByRole("row", { name: /Subject 2,/ });
    await userEvent.unhover(second);
    await userEvent.hover(second);
    await userEvent.click(second.getByRole("button", { name: "Archive" }));
    await userEvent.click(row);
    expect(api.actions).toEqual([
      { action: "unread", ids: [conversation(1).id] },
      { action: "archive", ids: [conversation(2).id] },
    ]);
    expect(router.state.location.pathname).toBe("/mail/inbox");
    // Then presses act again.
    await new Promise((done) => setTimeout(done, 500));
    await userEvent.click(row);
    await expect
      .poll(() => router.state.location.pathname)
      .toBe(`/mail/inbox/${conversation(1).id}`);
  });

  it("drop Enter and Space for a moment after a keyboard press, so a double press acts once", async () => {
    const api = mailApi([conversation(1, { unread: true })], one);
    await app(api);
    const row = page.getByRole("row", { name: /Subject 1,/ });
    row.element().focus();
    await userEvent.keyboard("{ArrowRight}{ArrowRight}{ArrowRight}");
    await expect.element(row.getByRole("button", { name: "Mark read" })).toHaveFocus();
    await userEvent.keyboard("{Enter}{Enter}");
    await userEvent.keyboard(" ");
    const mark = row.getByRole("button", { name: "Mark unread" });
    await expect.element(mark).toHaveFocus();
    expect(api.actions).toEqual([{ action: "read", ids: [conversation(1).id] }]);
    // Then the keys act again.
    await new Promise((done) => setTimeout(done, 500));
    await userEvent.keyboard("{Enter}");
    await expect.element(row.getByRole("button", { name: "Mark read" })).toHaveFocus();
    expect(api.actions).toEqual([
      { action: "read", ids: [conversation(1).id] },
      { action: "unread", ids: [conversation(1).id] },
    ]);
  });

  it("are left out of a row too narrow for them, e.g. at twice the text size", async () => {
    await app(mailApi([conversation(1)], one));
    const row = page.getByRole("row", { name: /Subject 1,/ });
    await expect.element(row).toBeVisible();
    document.documentElement.style.fontSize = "32px";
    try {
      // The list's 373 px at 1440 is under 16rem at 32 px text.
      await expect.poll(() => getComputedStyle(rowActions(/Subject 1,/)).display).toBe("none");
      row.element().focus();
      await userEvent.keyboard("{ArrowRight}");
      await expect.element(starOf(/Subject 1,/)).toHaveFocus();
    } finally {
      document.documentElement.style.fontSize = "";
    }
    await expect.poll(() => getComputedStyle(rowActions(/Subject 1,/)).display).toBe("flex");
  });

  it("are left out while the list is picking", async () => {
    await app(mailApi([conversation(1), conversation(2)], one));
    const row = page.getByRole("row", { name: /Subject 1,/ });
    row.element().focus();
    await userEvent.keyboard(" ");
    await expect.element(row).toHaveAttribute("aria-selected", "true");
    expect(document.querySelector("[data-slot=row-actions]")).toBeNull();
  });
});

describe("an unread row", () => {
  it("stands out by weight, a bar at its edge, and a read row's quieter text", async () => {
    await app(readAndUnread());
    const unread = page.getByRole("row", { name: /Subject 1,/ });
    await expect.element(unread).toBeVisible();
    const read = page.getByRole("row", { name: /Subject 2,/ }).element();

    expect(subjectStyle(1).fontWeight).toBe("600");
    expect(subjectStyle(2).fontWeight).toBe("400");
    expect(subjectStyle(2).color).not.toBe(subjectStyle(1).color);
    expect(bar(unread.element()).width).toBe("3px");
    expect(bar(unread.element()).backgroundColor).not.toBe(
      getComputedStyle(unread.element()).backgroundColor,
    );
    expect(bar(read).content).toBe("none");
  });

  it("is heavier in dark, whose text is drawn without macOS's thickening", async () => {
    document.documentElement.classList.add("dark");
    try {
      await app(readAndUnread());
      const unread = page.getByRole("row", { name: /Subject 1,/ });
      await expect.element(unread).toBeVisible();
      expect(subjectStyle(1).fontWeight).toBe("650");
      expect(getComputedStyle(document.body).getPropertyValue("-webkit-font-smoothing")).toBe(
        "antialiased",
      );
    } finally {
      document.documentElement.classList.remove("dark");
    }
  });

  it("draws its bar in the text's colour in forced colours", async () => {
    await app(readAndUnread());
    const unread = page.getByRole("row", { name: /Subject 1,/ });
    await expect.element(unread).toBeVisible();
    const session = cdp();
    await session.send("Emulation.setEmulatedMedia", {
      features: [{ name: "forced-colors", value: "active" }],
    });
    try {
      await expect.poll(() => bar(unread.element()).backgroundColor).toBe(subjectStyle(1).color);
    } finally {
      await session.send("Emulation.setEmulatedMedia", { features: [] });
    }
  });
});

describe("the Inbox chip", () => {
  it("marks a conversation still in the Inbox outside the Inbox, e.g. in All mail", async () => {
    await app(mailApi([conversation(1), conversation(2, { inbox: false })], one), "/mail/all");

    const inbox = page.getByRole("row", { name: /Subject 1, .*In Inbox/ });
    await expect.element(inbox).toBeVisible();
    await expect.element(inbox.getByText("Inbox", { exact: true })).toBeVisible();
    const archived = page.getByRole("row", { name: /Subject 2,/ });
    expect(archived.element().getAttribute("aria-label")).not.toContain("In Inbox");
    expect(archived.getByText("Inbox", { exact: true }).query()).toBeNull();
  });

  it("keeps its shape on a hovered row, whose surface is its own fill", async () => {
    await app(mailApi([conversation(1)], one), "/mail/all");
    const row = page.getByRole("row", { name: /Subject 1, .*In Inbox/ });
    const chip = row.getByText("Inbox", { exact: true });
    await expect.element(chip).toBeVisible();

    // From off the row: a pointer an earlier test left where this row now
    // is enters it without moving, which reports no hover.
    await userEvent.unhover(row);
    await userEvent.hover(row);
    await expect.element(row).toHaveAttribute("data-hovered", "true");
    // The chip, around its name.
    const badge = chip.element().closest("[data-slot=badge]")!;
    expect(getComputedStyle(badge).backgroundColor).not.toBe(
      getComputedStyle(row.element()).backgroundColor,
    );
  });

  it("marks nothing in the Inbox, where every row is", async () => {
    await app(mailApi([conversation(1)], one));

    const row = page.getByRole("row", { name: /Subject 1,/ });
    await expect.element(row).toBeVisible();
    expect(row.element().getAttribute("aria-label")).not.toContain("In Inbox");
    expect(row.getByText("Inbox", { exact: true }).query()).toBeNull();
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

  it("keeps its rows when a refresh finds the session ended", async () => {
    const api = mailApi([conversation(1)], one);
    const { client } = await app(api);
    const row = page.getByRole("row", { name: /Subject 1,/ });
    await expect.element(row).toBeVisible();
    api.signedOut = true;

    await refreshNewest(client);

    // The session-end rule answers, e.g. with a banner over a page kept to
    // copy from: the rows stay to copy from too.
    await new Promise((done) => setTimeout(done, 300));
    await expect.element(row).toBeVisible();
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
    await anchored(api);
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
    await anchored(api);
    api.conversations.push(conversation(100));
    await refreshNewest(client);
    const button = page.getByRole("button", { name: "1 new conversation" });
    await expect.element(button).toBeVisible();
    button.element().focus();
    await userEvent.keyboard("{Enter}");
    await expect.element(page.getByRole("row", { name: /Subject 100,/ })).toHaveFocus();
  });

  it("sends a quick star and unstar one after the other", async () => {
    const api = mailApi([conversation(1)], one);
    await app(api);
    const row = page.getByRole("row", { name: /Subject 1,/ });
    await expect.element(row).toBeVisible();
    await userEvent.hover(row);
    const star = starOf(/Subject 1,/);
    await userEvent.click(star);
    await userEvent.click(star);
    await vi.waitFor(() => expect(api.actions.map((a) => a.action)).toEqual(["star", "unstar"]), {
      timeout: 10_000,
    });
    await expect.element(star).toHaveAttribute("aria-pressed", "false");
    expect(api.conversations[0]?.starred).toBe(false);
  });
});

// checkAccounts returns two accounts inbok can use and one Google stopped
// accepting.
function checkAccounts() {
  return [
    listedAccount(1, "ana@example.com"),
    listedAccount(2, "ana@work.example"),
    {
      ...listedAccount(3, "ana@old.example"),
      status: "needs-reconnect" as const,
      allowedActions: ["reconnect" as const, "rename" as const, "disconnect" as const],
    },
  ];
}

describe("Check for new mail", () => {
  it("says which account it could not check, and checks the others", async () => {
    const api = mailApi([conversation(1)], checkAccounts());
    api.refuseSync[2] = { status: 409, type: "/api/v0/problems/account-needs-reconnect" };
    await app(api);

    await userEvent.click(page.getByRole("button", { name: "Check for new mail" }));

    await expect.element(page.getByText("Couldn't check for new mail")).toBeVisible();
    await expect
      .element(page.getByText("Reconnect ana@work.example in Connected accounts to get its mail."))
      .toBeVisible();
    expect(api.syncs).toEqual([1]);
  });

  it("says the browser is offline instead of checking", async () => {
    const api = mailApi([conversation(1)], checkAccounts());
    await app(api);
    onlineManager.setOnline(false);
    try {
      await userEvent.click(page.getByRole("button", { name: "Check for new mail" }));

      await expect
        .element(page.getByText("You're offline, so inbok can't check for new mail."))
        .toBeVisible();
      expect(api.syncs).toEqual([]);
    } finally {
      onlineManager.setOnline(true);
    }
  });

  it("does nothing more once the member signs out while it checks", async () => {
    const api = mailApi([conversation(1)], checkAccounts());
    // A refusal would show a toast naming the previous member's account.
    api.refuseSync[2] = { status: 409, type: "/api/v0/problems/account-needs-reconnect" };
    let answer: (() => void) | undefined;
    const held = new Promise<void>((resolve) => {
      answer = resolve;
    });
    let asked = 0;
    const fetch = api.fetch;
    api.fetch = async (input, init) => {
      if (init?.method === "POST") {
        asked++;
        await held;
      }
      return fetch(input, init);
    };
    const { client } = await app(api);
    await userEvent.click(page.getByRole("button", { name: "Check for new mail" }));
    await expect.poll(() => asked).toBe(2);

    // Sign-out: the page goes and the member's data ends; the next page,
    // e.g. sign-in, has the app's toasts.
    await cleanup();
    clearAccountCache(client);
    current = undefined;
    await render(<Toaster />);
    answer?.();
    await held;
    await new Promise((resolve) => setTimeout(resolve, 100));

    // No toast names the previous member's account, and no mail came back
    // for them, nor its refresh timers.
    expect(page.getByText("Couldn't check for new mail").elements()).toHaveLength(0);
    expect(currentMail(client)).toBeUndefined();
  });

  it("takes a check started moments ago as started, and reads the mail again soon", async () => {
    const api = mailApi([conversation(1)], checkAccounts());
    api.refuseSync[1] = { status: 429, type: "/api/v0/problems/sync-throttled" };
    // Only the throttled account, so the re-read comes from it alone.
    await app(api, "/mail/inbox?account=1");
    await expect.element(grid().getByRole("row").first()).toBeVisible();
    // The list's pages, and the accounts, whose status the page shows.
    const lists = () =>
      api.requests.filter((request) => request.startsWith("/api/v0/conversations?")).length;
    const accounts = () =>
      api.requests.filter((request) => request === "/api/v0/connected-accounts").length;
    const listsBefore = lists();
    const accountsBefore = accounts();

    await userEvent.click(page.getByRole("button", { name: "Check for new mail" }));

    // The first re-read comes 2 s after the check, well before the lists'
    // own 30 s timer. The counts come with it where a page shows them, e.g.
    // in the sidebar; this one shows none.
    await expect.poll(lists, { timeout: 4000 }).toBeGreaterThan(listsBefore);
    expect(accounts()).toBeGreaterThan(accountsBefore);
    expect(api.syncs).toEqual([]);
    expect(page.getByText("Couldn't check for new mail").elements()).toHaveLength(0);
  });

  it("says so when no account the list shows can be checked", async () => {
    const api = mailApi([conversation(1, { accountId: 3 })], checkAccounts());
    await app(api, "/mail/inbox?account=3");

    await userEvent.click(page.getByRole("button", { name: "Check for new mail" }));

    await expect.element(page.getByText("Couldn't check for new mail")).toBeVisible();
    expect(api.syncs).toEqual([]);
  });
});
