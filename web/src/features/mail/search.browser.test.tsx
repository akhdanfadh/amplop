import { RouterProvider } from "@tanstack/react-router";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { cleanup, render } from "vitest-browser-react";
import type { ConversationRow, PlaceCount } from "@/api/generated/model";
import { closeToasts } from "@/components/ui/toast";
import { clearAccountCache } from "@/features/auth/session-cache";
import { appRouter } from "@/test/app-router";
import { conversation, listedAccount, mailApi, type MailApi } from "@/test/mail-api";

const label = "11111111-1111-4111-8111-111111111111";

let api: MailApi;
let current: Awaited<ReturnType<typeof appRouter>> | undefined;

afterEach(async () => {
  closeToasts();
  await cleanup();
  if (current) clearAccountCache(current.options.context.queryClient);
  current = undefined;
  vi.unstubAllGlobals();
});

// rows are the mail the tests search: two invoices, one in a label, and
// a lunch.
const rows: ConversationRow[] = [
  conversation(1, { subject: "Invoice from Shop" }),
  conversation(2, { subject: "Lunch on Friday" }),
  conversation(3, { subject: "Your invoice", labelIds: [label], inbox: false }),
];

// open renders the app width wide at address over rows, one account's,
// with a label, Receipts, and more labels when given, and returns its
// router.
async function open(address: string, width = 1440, more: PlaceCount[] = []) {
  await page.viewport(width, 900);
  api = mailApi(
    rows.map((row) => ({ ...row })),
    [listedAccount(1, "ana@example.com")],
  );
  api.places = [{ accountId: 1, name: "Receipts", parent: null, place: label, unread: 0 }, ...more];
  const router = await appRouter(address, api.server);
  await render(<RouterProvider router={router} />);
  current = router;
  return router;
}

// openOver renders the app at address over conversations, of accounts
// (one when left out), with each later search page starting shift results
// early.
async function openOver(
  address: string,
  conversations: ConversationRow[],
  shift = 0,
  accounts = [listedAccount(1, "ana@example.com")],
) {
  await page.viewport(1440, 900);
  api = mailApi(conversations, accounts);
  api.searchShift = shift;
  const router = await appRouter(address, api.server);
  await render(<RouterProvider router={router} />);
  current = router;
  return router;
}

// openWithManyPlaces renders a search of twenty places, the API's limit:
// four views and sixteen of seventeen labels, neither Spam nor Trash.
async function openWithManyPlaces() {
  const many = Array.from(
    { length: 17 },
    (_, n) => `22222222-2222-4222-8222-${String(n + 1).padStart(12, "0")}`,
  );
  const picked = ["inbox", "starred", "sent", "all", ...many.slice(0, 16)];
  return open(
    `/mail/search?q=invoice&place=${encodeURIComponent(JSON.stringify(picked))}`,
    1440,
    many.map((place, n) => ({
      accountId: 1,
      name: `Label ${String(n + 1).padStart(2, "0")}`,
      parent: null,
      place,
      unread: 0,
    })),
  );
}

// toEnd scrolls grid to its end and returns how many second pages the
// search has asked for, for a poll that scrolls until the list loads more.
function toEnd(grid: Element): number {
  grid.scrollTop = grid.scrollHeight;
  return searches().filter((request) => request.includes("page=2")).length;
}

// rowsAtEnd scrolls grid to its end and returns how many rows it says the
// list holds, for a poll that scrolls until every page is in: each page
// that arrives moves the end further down.
function rowsAtEnd(grid: Element): string | null {
  grid.scrollTop = grid.scrollHeight;
  return grid.getAttribute("aria-rowcount");
}

function box() {
  return page.getByRole("searchbox", { name: "Search mail" });
}

function results() {
  return page.getByRole("grid", { name: "Results" });
}

// pickedBar returns the picking bar.
function pickedBar() {
  return page.getByRole("toolbar", { name: "Picked conversations" });
}

// pickResult picks the result named like name by its box.
async function pickResult(name: RegExp): Promise<void> {
  const row = results().getByRole("row", { name });
  await userEvent.hover(row);
  const pick = row.element().querySelector("[data-slot=pick-box]");
  if (!(pick instanceof HTMLElement)) throw new Error("the result has no box");
  await userEvent.click(pick);
}

// searches returns the searches api was asked, as path and search.
function searches(): string[] {
  return api.requests.filter((request) => request.startsWith("/api/v0/conversations/search"));
}

describe("search", () => {
  it("shows results once two characters rest, keeping the box focused", async () => {
    const router = await open("/mail/inbox");
    await userEvent.click(box());
    await userEvent.keyboard("i");
    // One character searches nothing.
    await new Promise((done) => setTimeout(done, 400));
    expect(searches()).toEqual([]);
    expect(router.state.location.pathname).toBe("/mail/inbox");

    await userEvent.keyboard("nv");
    await expect.element(results()).toBeVisible();
    expect(router.state.location.pathname).toBe("/mail/search");
    expect(router.state.location.search).toEqual({ q: "inv" });
    await expect.element(box()).toHaveFocus();
    await expect.element(results().getByRole("row", { name: /invoice/i })).toHaveLength(2);
  });

  it("drops typing not yet searched when the member opens another place", async () => {
    const router = await open("/mail/inbox");
    // In one step, so the press lands within the 200 ms the box waits,
    // however busy the machine.
    await userEvent.fill(box(), "invoice");
    await userEvent.click(page.getByRole("link", { name: "Starred" }));
    await new Promise((done) => setTimeout(done, 450));
    expect(router.state.location.pathname).toBe("/mail/starred");
    await expect.element(box()).toHaveValue("");
  });

  it("shows the words of a search Back returns to, not what was being typed", async () => {
    const router = await open("/mail/search?q=invoice");
    await router.navigate({
      to: "/mail/$place",
      params: { place: "search" },
      search: { q: "lunch" },
    });
    await userEvent.click(box());
    await userEvent.keyboard(" plan");
    // Within the 200 ms the box waits, the history moves back.
    router.history.back();
    await new Promise((done) => setTimeout(done, 450));
    expect(router.state.location.search).toEqual({ q: "invoice" });
    await expect.element(box()).toHaveValue("invoice");
  });

  it("says when the words are longer than the API takes, and sends nothing", async () => {
    await open(`/mail/search?q=${"a".repeat(201)}`);
    await expect.element(page.getByText("Search with 200 characters or fewer.")).toBeVisible();
    expect(searches()).toEqual([]);
  });

  it("doesn't offer Spam and Trash past the API's limit of places", async () => {
    await openWithManyPlaces();
    // Spam and Trash would make 22: not offered.
    await expect.element(page.getByText(/Spam and Trash are not searched/)).toBeVisible();
    await expect
      .element(page.getByRole("button", { name: "Search them too" }))
      .not.toBeInTheDocument();
  });

  it("takes no more places in the menu at the API's limit, saying so", async () => {
    await openWithManyPlaces();
    await userEvent.click(page.getByRole("button", { name: /Inbox, Starred and 18 more/ }));
    await expect.element(page.getByText("Search up to 20 places at once.")).toBeVisible();
    await expect
      .element(page.getByRole("menuitemcheckbox", { name: "Label 17" }))
      .toHaveAttribute("aria-disabled", "true");
    await expect
      .element(page.getByRole("menuitemcheckbox", { name: "Label 01" }))
      .not.toHaveAttribute("aria-disabled");
  });

  it("keeps the list drawn and scrolled as the next page arrives", async () => {
    const many = Array.from({ length: 120 }, (_, n) =>
      conversation(n + 1, { subject: `Invoice ${n + 1}` }),
    );
    await openOver("/mail/search?q=invoice", many);
    await expect.element(results().getByRole("row", { name: /Invoice 1\b/ })).toBeVisible();
    const grid = results().element();
    await expect.poll(() => toEnd(grid)).toBe(1);
    await expect.poll(() => Number(grid.getAttribute("aria-rowcount"))).toBeGreaterThan(50);
    // The same list, still scrolled down: never swapped for placeholders.
    expect(results().element()).toBe(grid);
    expect(grid.scrollTop).toBeGreaterThan(0);
  });

  it("keeps the last results on screen until the next arrive", async () => {
    await open("/mail/search?q=lunch");
    await expect.element(results().getByRole("row", { name: /Lunch/ })).toBeVisible();
    const grid = results().element();
    await userEvent.fill(box(), "invoice");
    await expect.element(results().getByRole("row", { name: /Your invoice/ })).toBeVisible();
    expect(results().element()).toBe(grid);
  });

  it("shows a conversation once when a later page repeats it", async () => {
    const many = Array.from({ length: 120 }, (_, n) =>
      conversation(n + 1, { subject: `Invoice ${n + 1}` }),
    );
    const errors = vi.spyOn(console, "error");
    await openOver("/mail/search?q=invoice", many, 1);
    await expect.element(results().getByRole("row", { name: /Invoice 1\b/ })).toBeVisible();
    const grid = results().element();
    // Every page in: page 2 repeated page 1's last, page 3 page 2's.
    await expect.poll(() => rowsAtEnd(grid)).toBe("120");
    // Two rows of one key would have React say so.
    expect(errors.mock.calls.flat().join(" ")).not.toMatch(/same key/i);
  });

  it("pages on past a page whose hits are gone, to the total's last page", async () => {
    const many = Array.from({ length: 120 }, (_, n) =>
      conversation(n + 1, { subject: `Invoice ${n + 1}` }),
    );
    await openOver("/mail/search?q=invoice", many);
    api.searchDropped = [2];
    await expect.element(results().getByRole("row", { name: /Invoice 1\b/ })).toBeVisible();
    const grid = results().element();
    // Page 2 came back empty; page 3 still holds 20 of the 120.
    await expect.poll(() => rowsAtEnd(grid)).toBe("70");
    await expect.element(page.getByText("That's every match.")).toBeInTheDocument();
    // Three pages of 50 cover 120: no fourth is asked for.
    expect(searches().filter((request) => request.includes("page=4"))).toEqual([]);
  });

  it("marks the matched words where the API placed them", async () => {
    await open("/mail/search?q=invoice");
    const row = results().getByRole("row", { name: /Invoice from Shop/ });
    await expect.element(row).toBeVisible();
    const marks = [...row.element().querySelectorAll("mark")].map((mark) => mark.textContent);
    expect(marks).toEqual(["Invoice"]);
  });

  it("marks one place at most on a result, then how many more, so the snippet keeps its room", async () => {
    const travel = "22222222-2222-4222-8222-222222222222";
    await page.viewport(1440, 900);
    api = mailApi(
      [conversation(1, { subject: "Invoice from Shop", labelIds: [label, travel] })],
      [listedAccount(1, "ana@example.com")],
    );
    api.places = [
      { accountId: 1, name: "Receipts", parent: null, place: label, unread: 0 },
      { accountId: 1, name: "Travel", parent: null, place: travel, unread: 0 },
    ];
    const router = await appRouter("/mail/search?q=invoice", api.server);
    await render(<RouterProvider router={router} />);
    current = router;
    const row = results().getByRole("row", { name: /Invoice from Shop/ });
    await expect.element(row).toHaveAccessibleName(/Labels Receipts and Travel$/);

    const marks = row.element().querySelector("[aria-hidden=true][title]");
    const shown = [...(marks?.children ?? [])]
      .filter((mark) => mark.checkVisibility())
      .map((mark) => mark.textContent);
    expect(shown).toEqual(["Inbox", "+2"]);
  });

  it("picks results, the bar taking the sentence's place, and acts on the ones it changes", async () => {
    await open("/mail/search?q=invoice");
    await pickResult(/Invoice from Shop/);
    await pickResult(/Your invoice/);

    // The box keeps the words; the sentence and its count wait.
    await expect.element(pickedBar()).toBeVisible();
    await expect.element(pickedBar().getByText("All 2", { exact: true })).toBeVisible();
    await expect.element(box()).toHaveValue("invoice");
    expect(page.getByText(/^Searching/).query()).toBeNull();

    // One is in the Inbox, the other archived: Archive takes only the first,
    // and both stay, as the search looks in All mail.
    await userEvent.click(pickedBar().getByRole("button", { name: "Archive" }));
    await expect.poll(() => api.actions).toEqual([{ action: "archive", ids: [rows[0].id] }]);
    await expect
      .element(results().getByRole("row", { name: /Invoice from Shop/ }))
      .toHaveAttribute("aria-selected", "true");

    await userEvent.keyboard("{Escape}");
    await expect.element(pickedBar()).not.toBeInTheDocument();
    await expect.element(page.getByText(/^Searching/)).toBeVisible();
  });

  it("puts the bar in a phone's top bar, the box coming back when picking stops", async () => {
    await open("/mail/search?q=invoice", 390);
    await pickResult(/Invoice from Shop/);

    await expect.element(pickedBar()).toBeVisible();
    expect(box().query()).toBeNull();
    expect(page.getByText(/^Searching/).query()).toBeNull();

    await userEvent.click(pickedBar().getByRole("button", { name: "Stop picking" }));
    await expect.element(box()).toHaveValue("invoice");
    await expect.element(page.getByText(/^Searching/)).toBeVisible();
    // Focus goes back to the results, as in a place's list: the box coming
    // back doesn't take it.
    await expect.poll(() => results().element().contains(document.activeElement)).toBe(true);
    await expect.element(box()).not.toHaveFocus();
  });

  it("keeps the sentence while every picked result an action took waits for its answer", async () => {
    await open("/mail/search?q=invoice");
    await pickResult(/Invoice from Shop/);
    // The action's answer waits until let go.
    const waiting: (() => void)[] = [];
    api.server.on("POST", "/api/v0/conversations/actions", async (_, earlier) => {
      await new Promise<void>((done) => waiting.push(done));
      return earlier();
    });

    await userEvent.click(pickedBar().getByRole("button", { name: "Delete" }));
    await expect
      .element(results().getByRole("row", { name: /Invoice from Shop/ }))
      .not.toBeInTheDocument();
    // No bar with nothing in it, and one heading: the place page's.
    await expect.element(pickedBar()).not.toBeInTheDocument();
    await expect.element(page.getByText(/^Searching/)).toBeVisible();
    expect(document.querySelectorAll("#place-heading")).toHaveLength(1);
    for (const done of waiting) done();
    await expect.poll(() => api.actions).toEqual([{ action: "trash", ids: [rows[0].id] }]);
  });

  it("searches the latest words submitted while picking's Back is still under way", async () => {
    const router = await open("/mail/inbox");
    await userEvent.fill(box(), "invoice");
    await expect.element(results().getByRole("row", { name: /Invoice from Shop/ })).toBeVisible();
    await pickResult(/Invoice from Shop/);
    await expect.element(pickedBar()).toBeVisible();
    // A browser's Back lands a moment later; this one, later still.
    const back = router.history.back.bind(router.history);
    router.history.back = () => {
      setTimeout(back, 300);
    };

    await userEvent.fill(box(), "invoice shop");
    await userEvent.keyboard("{Enter}");
    await userEvent.fill(box(), "your invoice");
    await userEvent.keyboard("{Enter}");

    await expect.poll(() => router.state.location.search).toMatchObject({ q: "your invoice" });
    await new Promise((done) => setTimeout(done, 500));
    expect(router.state.location.search).toMatchObject({ q: "your invoice" });
    router.history.back = back;
    router.history.back();
    await expect.poll(() => router.state.location.pathname).toBe("/mail/inbox");
  });

  it("ends picking before new words search, so Back leaves the search", async () => {
    const router = await open("/mail/inbox");
    await userEvent.fill(box(), "invoice");
    await expect.element(results().getByRole("row", { name: /Invoice from Shop/ })).toBeVisible();
    await pickResult(/Invoice from Shop/);
    await expect.element(pickedBar()).toBeVisible();

    // The result matches the new words too, but they are a new search.
    await userEvent.fill(box(), "invoice shop");
    await expect.poll(() => router.state.location.search).toMatchObject({ q: "invoice shop" });
    await expect.element(pickedBar()).not.toBeInTheDocument();
    await expect
      .element(results().getByRole("row", { name: /Invoice from Shop/ }))
      .not.toHaveAttribute("aria-selected", "true");

    router.history.back();
    await expect.poll(() => router.state.location.pathname).toBe("/mail/inbox");
  });

  it("takes a deleted result out, focus going to the one after it", async () => {
    await open("/mail/search?q=invoice");
    await pickResult(/Invoice from Shop/);

    await userEvent.click(pickedBar().getByRole("button", { name: "Delete" }));
    await expect
      .element(results().getByRole("row", { name: /Invoice from Shop/ }))
      .not.toBeInTheDocument();
    await expect.element(results().getByRole("row", { name: /Your invoice/ })).toHaveFocus();
  });

  it("opens a result from its text while picking, as a place's list does", async () => {
    const router = await open("/mail/search?q=invoice");
    await pickResult(/Invoice from Shop/);

    await userEvent.click(
      results()
        .getByRole("row", { name: /Your invoice/ })
        .getByText("Your invoice"),
    );
    await expect.poll(() => router.state.location.pathname).toBe(`/mail/search/${rows[2].id}`);
    // At the message that matched, the search kept.
    expect(router.state.location.search).toMatchObject({
      q: "invoice",
      message: rows[2].id.replace("00000000-0000-4000-8000", "11111111-1111-4111-8111"),
    });
    await expect
      .element(results().getByRole("row", { name: /Invoice from Shop/ }))
      .toHaveAttribute("aria-selected", "true");
    // Back returns to the results still picking; the next Back stops it.
    router.history.back();
    await expect.poll(() => router.state.location.pathname).toBe("/mail/search");
    await expect.element(pickedBar()).toBeVisible();
    router.history.back();
    await expect.element(pickedBar()).not.toBeInTheDocument();
  });

  it("searches the label it starts in, and names it in the box", async () => {
    const router = await open(`/mail/${label}?account=1`);
    await expect.element(box()).toHaveAttribute("placeholder", "Search Receipts");
    await userEvent.click(box());
    await userEvent.keyboard("invoice");
    await expect.element(results()).toBeVisible();
    expect(router.state.location.search).toEqual({ account: 1, q: "invoice", place: [label] });
    await expect.element(results().getByRole("row", { name: /invoice/i })).toHaveLength(1);
    expect(searches().at(-1)).toContain(`place=${label}`);
  });

  it("returns to the place it started from on Escape, the box still focused", async () => {
    const router = await open(`/mail/${label}?account=1`);
    await userEvent.click(box());
    await userEvent.keyboard("invoice");
    await expect.element(results()).toBeVisible();
    await userEvent.keyboard("{Escape}");
    await expect.poll(() => router.state.location.pathname).toBe(`/mail/${label}`);
    await expect.element(box()).toHaveValue("");
    await expect.element(box()).toHaveFocus();
  });

  it("returns to the place with the box focused after its clear button", async () => {
    const router = await open("/mail/inbox");
    await userEvent.click(box());
    await userEvent.keyboard("lunch");
    await expect.element(results()).toBeVisible();
    await userEvent.click(page.getByRole("button", { name: "Clear search" }));
    await expect.poll(() => router.state.location.pathname).toBe("/mail/inbox");
    await expect.element(box()).toHaveFocus();
  });

  it("refuses a typed day after today, saying why", async () => {
    const router = await open("/mail/search?q=invoice");
    await userEvent.click(page.getByRole("button", { name: "any time" }));
    await userEvent.click(page.getByRole("spinbutton", { name: /^day, Since/ }));
    await userEvent.keyboard("20112099");
    await expect.element(page.getByText("Pick days up to today.")).toBeVisible();
    await expect.element(page.getByRole("button", { name: "Apply" })).toBeDisabled();
    expect(router.state.location.search).toEqual({ q: "invoice" });
  });

  it("waits for a day typed only in part, the year still empty", async () => {
    const router = await open("/mail/search?q=invoice");
    await userEvent.click(page.getByRole("button", { name: "any time" }));
    await userEvent.click(page.getByRole("spinbutton", { name: /^day, Since/ }));
    await userEvent.keyboard("0211");
    await expect.element(page.getByText("Finish typing the day, or clear it.")).toBeVisible();
    await expect.element(page.getByRole("button", { name: "Apply" })).toBeDisabled();
    expect(router.state.location.search).toEqual({ q: "invoice" });
  });

  it("waits for a year deleted to be typed again", async () => {
    await open("/mail/search?q=invoice&since=2025-11-02");
    await userEvent.click(page.getByRole("button", { name: /^since 2 Nov 2025/ }));
    await userEvent.click(page.getByRole("spinbutton", { name: /^year, Since/ }));
    await userEvent.keyboard("{Backspace}{Backspace}{Backspace}{Backspace}{Backspace}");
    await expect.element(page.getByRole("button", { name: "Apply" })).toBeDisabled();
  });

  it("rings the From field while its input has keyboard focus", async () => {
    await open("/mail/search?q=invoice");
    await userEvent.click(page.getByRole("button", { name: "anyone" }));
    const input = page.getByRole("dialog", { name: "From" }).getByRole("textbox");
    await expect.element(input).toHaveFocus();
    // To Done and back, by keyboard.
    await userEvent.tab();
    await userEvent.tab({ shift: true });
    await expect.element(input).toHaveFocus();
    const field = input.element().closest(".rounded-lg");
    expect(field && getComputedStyle(field).outlineStyle).toBe("solid");
  });

  it("opens a result at the message that matched", async () => {
    const router = await open("/mail/search?q=lunch");
    await userEvent.click(results().getByRole("row", { name: /Lunch on Friday/ }));
    await expect.poll(() => router.state.location.pathname).toBe(`/mail/search/${rows[1].id}`);
    expect(router.state.location.search).toMatchObject({
      q: "lunch",
      message: "11111111-1111-4111-8111-000000000002",
    });
  });

  it("leaves an open result's message out of the search it changes", async () => {
    const router = await open(
      `/mail/search/${rows[1].id}?q=lunch&message=11111111-1111-4111-8111-000000000002`,
    );
    await userEvent.click(page.getByRole("button", { name: "Search them too" }));
    await expect
      .poll(() => router.state.location.search)
      .toEqual({ q: "lunch", place: ["all", "spam", "trash"] });
  });

  it("stars a result at once", async () => {
    await open("/mail/search?q=lunch");
    const star = results().getByRole("button", { name: "Star" });
    await userEvent.click(star);
    await expect.element(star).toHaveAttribute("aria-pressed", "true");
    await expect.poll(() => api.actions).toEqual([{ action: "star", ids: [rows[1].id] }]);
  });

  it("offers each result its own actions on hover, as its reading pane would", async () => {
    await open("/mail/search?q=invoice");
    const archived = results().getByRole("row", { name: /Your invoice/ });
    await userEvent.hover(archived);
    const names = [...archived.element().querySelectorAll("button")].map((b) => b.ariaLabel);
    expect(names).toEqual(["Move to Inbox", "Delete", "Mark unread", "Star"]);

    const inInbox = results().getByRole("row", { name: /Invoice from Shop/ });
    await userEvent.hover(inInbox);
    await userEvent.click(inInbox.getByRole("button", { name: "Archive" }));
    await expect.poll(() => api.actions).toEqual([{ action: "archive", ids: [rows[0].id] }]);
    // Still in All mail, which the search looks in.
    await expect.element(inInbox.getByRole("button", { name: "Move to Inbox" })).toBeVisible();
  });

  it("says what found nothing, with a way to clear the filters", async () => {
    const router = await open("/mail/search?q=parcel&has=attachment");
    await expect
      .element(page.getByRole("heading", { name: 'Nothing matches "parcel"' }))
      .toBeVisible();
    await userEvent.click(page.getByRole("button", { name: "Clear filters" }));
    await expect.poll(() => router.state.location.search).toEqual({ q: "parcel" });
  });

  it("names every account still syncing when it found nothing", async () => {
    const work = listedAccount(1, "ana@example.com");
    work.sync.status = "running";
    const home = listedAccount(2, "ana@home.example", 2);
    home.sync.status = "waiting";
    await openOver("/mail/search?q=parcel", [], 0, [work, home]);
    await expect
      .element(
        page.getByText(
          "ana@example.com and ana@home.example are still syncing, so some of their mail can't be found yet.",
        ),
      )
      .toBeVisible();
  });

  it("offers Spam and Trash when it found nothing without filters", async () => {
    const router = await open("/mail/search?q=parcel");
    await userEvent.click(page.getByRole("button", { name: "Search Spam and Trash too" }));
    await expect
      .poll(() => router.state.location.search)
      .toEqual({ q: "parcel", place: ["all", "spam", "trash"] });
  });

  it("says when search isn't answering, with Try again", async () => {
    await open("/mail/inbox");
    api.failSearch = 503;
    await userEvent.click(box());
    await userEvent.keyboard("lunch");
    await expect
      .element(page.getByText("Search isn't answering right now. Try again in a moment."))
      .toBeVisible();
    api.failSearch = undefined;
    await userEvent.click(page.getByRole("button", { name: "Try again" }));
    await expect.element(results().getByRole("row", { name: /Lunch/ })).toBeVisible();
  });

  it("changes the places from the sentence's menu", async () => {
    const router = await open("/mail/search?q=invoice");
    await userEvent.click(page.getByRole("button", { name: "all mail" }));
    await userEvent.click(page.getByRole("menuitemcheckbox", { name: "Receipts" }));
    await userEvent.click(page.getByRole("menuitemcheckbox", { name: "Inbox" }));
    // In the menu's order, whatever order they were picked in.
    await expect
      .poll(() => router.state.location.search)
      .toEqual({ q: "invoice", place: ["inbox", label] });
    await userEvent.keyboard("{Escape}");
    await expect.element(page.getByRole("button", { name: "Inbox and Receipts" })).toBeVisible();
  });

  it("picks a date relative to today", async () => {
    const router = await open("/mail/search?q=invoice");
    await userEvent.click(page.getByRole("button", { name: "any time" }));
    await userEvent.click(page.getByRole("button", { name: "Past 7 days" }));
    await expect.poll(() => router.state.location.search).toEqual({ q: "invoice", date: "7d" });
    await expect.element(page.getByRole("button", { name: "in the past 7 days" })).toBeVisible();
    expect(searches().at(-1)).toContain("since=");
  });

  it("takes a range typed as days, Before left out", async () => {
    const router = await open("/mail/search?q=invoice");
    await userEvent.click(page.getByRole("button", { name: "any time" }));
    // The Since field is the first focused; days are British, day first.
    await userEvent.click(page.getByRole("spinbutton", { name: /^day, Since/ }));
    await userEvent.keyboard("02112025");
    await userEvent.click(page.getByRole("spinbutton", { name: /^day, Before/ }));
    await userEvent.keyboard("10112025");
    await userEvent.click(page.getByRole("button", { name: "Apply" }));
    await expect
      .poll(() => router.state.location.search)
      .toEqual({ q: "invoice", since: "2025-11-02", before: "2025-11-10" });
    await expect
      .element(page.getByRole("button", { name: "since 2 Nov 2025 and before 10 Nov 2025" }))
      .toBeVisible();
  });

  it("keeps a long unbroken search word inside the empty title", async () => {
    const word = "Rechnungsbenachrichtigungseinstellungsänderungsbestätigung".repeat(2);
    await open(`/mail/search?q=${word}`, 320);
    const title = page.getByRole("heading", { name: /Nothing matches/ });
    await expect.element(title).toBeVisible();
    const heading = title.element();
    expect(heading.scrollWidth).toBeLessThanOrEqual(heading.clientWidth);
  });

  it("scrolls the date chooser on a short phone, so Apply stays in reach", async () => {
    const router = await open("/mail/search?q=invoice", 320);
    await page.viewport(320, 568);
    await userEvent.click(page.getByRole("button", { name: "any time" }));
    const apply = page.getByRole("button", { name: "Apply" });
    apply.element().scrollIntoView({ block: "nearest" });
    const { bottom } = apply.element().getBoundingClientRect();
    expect(bottom).toBeLessThanOrEqual(window.innerHeight);
    await userEvent.click(page.getByRole("spinbutton", { name: /^day, Since/ }));
    await userEvent.keyboard("02112025");
    await userEvent.click(apply);
    await expect.poll(() => router.state.location.search).toMatchObject({ since: "2025-11-02" });
  });

  it("cuts a long sender address in the sentence, whole in its tooltip", async () => {
    const address = "bartholomew.fitzgerald@northwind-industries-holdings.example.com";
    await open(`/mail/search?q=invoice&from=${encodeURIComponent(JSON.stringify([address]))}`, 390);
    const phrase = page.getByRole("button", { name: address });
    await expect.element(phrase).toBeVisible();
    expect(phrase.element().getBoundingClientRect().width).toBeLessThanOrEqual(390);
    await userEvent.hover(phrase);
    await expect.element(page.getByRole("tooltip")).toHaveTextContent(address);
  });

  it("opens from the phone's top bar and gives focus back on leaving", async () => {
    const router = await open("/mail/inbox", 390);
    const opener = page.getByRole("button", { name: "Search", exact: true });
    await userEvent.click(opener);
    await expect.element(box()).toHaveFocus();
    await userEvent.keyboard("lunch");
    await expect.element(results()).toBeVisible();
    await userEvent.click(page.getByRole("button", { name: "Close search" }));
    await expect.poll(() => router.state.location.pathname).toBe("/mail/inbox");
    await expect.element(page.getByRole("button", { name: "Search", exact: true })).toHaveFocus();
  });
});
