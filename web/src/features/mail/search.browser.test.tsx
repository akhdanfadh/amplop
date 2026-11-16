import { RouterProvider } from "@tanstack/react-router";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { cleanup, render } from "vitest-browser-react";
import type { ConversationRow, PlaceCount } from "@/api/generated/model";
import { closeToasts } from "@/components/ui/toast";
import { clearAccountCache } from "@/features/auth/session-cache";
import { appRouter, memberSession } from "@/test/app-router";
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
  const router = await appRouter(address, memberSession, (url, init) =>
    new URL(url, location.href).pathname === "/api/v0/sessions/current"
      ? undefined
      : api.fetch(url, init),
  );
  await render(<RouterProvider router={router} />);
  current = router;
  return router;
}

// openOver renders the app at address over conversations, one account's,
// with each later search page starting shift results early.
async function openOver(address: string, conversations: ConversationRow[], shift = 0) {
  await page.viewport(1440, 900);
  api = mailApi(conversations, [listedAccount(1, "ana@example.com")]);
  api.searchShift = shift;
  const router = await appRouter(address, memberSession, (url, init) =>
    new URL(url, location.href).pathname === "/api/v0/sessions/current"
      ? undefined
      : api.fetch(url, init),
  );
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

  it("marks the matched words where the API placed them", async () => {
    await open("/mail/search?q=invoice");
    const row = results().getByRole("row", { name: /Invoice from Shop/ });
    await expect.element(row).toBeVisible();
    const marks = [...row.element().querySelectorAll("mark")].map((mark) => mark.textContent);
    expect(marks).toEqual(["Invoice"]);
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

  it("says what found nothing, with a way to clear the filters", async () => {
    const router = await open("/mail/search?q=parcel&has=attachment");
    await expect
      .element(page.getByRole("heading", { name: 'Nothing matches "parcel"' }))
      .toBeVisible();
    await userEvent.click(page.getByRole("button", { name: "Clear filters" }));
    await expect.poll(() => router.state.location.search).toEqual({ q: "parcel" });
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

  it("keeps a long unbroken search word inside the empty title", async () => {
    const word = "Rechnungsbenachrichtigungseinstellungsänderungsbestätigung".repeat(2);
    await open(`/mail/search?q=${word}`, 320);
    const title = page.getByRole("heading", { name: /Nothing matches/ });
    await expect.element(title).toBeVisible();
    const heading = title.element();
    expect(heading.scrollWidth).toBeLessThanOrEqual(heading.clientWidth);
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
