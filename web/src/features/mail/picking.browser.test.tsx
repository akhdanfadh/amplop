import { RouterProvider } from "@tanstack/react-router";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import type { ConversationRow } from "@/api/generated/model";
import { appRouter } from "@/test/app-router";
import { fakeClock } from "@/test/clock";
import { conversation, listedAccount, mailApi, type MailApi } from "@/test/mail-api";

let api: MailApi;

afterEach(() => {
  gated = false;
  waiting = [];
});

// Whether open's actions wait to be answered, and the ones waiting, each
// answered when its function runs.
let gated = false;
let waiting: (() => void)[] = [];

// open renders the app width wide at address over rows, one account's, or
// accounts', and returns its router once the list shows.
async function open(
  rows: ConversationRow[],
  { address = "/mail/inbox", width = 1440, accounts = [listedAccount(1, "ana@example.com")] } = {},
) {
  await page.viewport(width, 900);
  api = mailApi(rows, accounts);
  api.server.on("POST", "/api/v0/conversations/actions", async (_, earlier) => {
    if (gated) await new Promise<void>((done) => waiting.push(done));
    return earlier();
  });
  const router = await appRouter(address, api.server);
  await render(<RouterProvider router={router} />);
  await expect.element(list()).toBeVisible();
  return router;
}

// settled waits until router has loaded the page it is going to. A test
// waits for it before it ends: a navigation left loading reads its
// conversation after the test's fake server is gone.
async function settled(router: { state: { status: string } }): Promise<void> {
  await expect.poll(() => router.state.status).toBe("idle");
}

// list returns the conversation list.
function list() {
  return page.getByRole("grid", { name: "Conversations" });
}

// row returns the row of the conversation subject names.
function row(subject: string) {
  return list().getByRole("row", { name: new RegExp(`${subject},`) });
}

// box returns the box at the start of subject's row, which picks it.
function box(subject: string): HTMLElement {
  const element = row(subject).element().querySelector<HTMLElement>("[data-slot=pick-box]");
  if (!element) throw new Error(`No box on ${subject}'s row.`);
  return element;
}

// pickBox presses the box of subject's row, with Shift when shift is set.
async function pickBox(subject: string, { shift = false } = {}): Promise<void> {
  // Playwright's own click option, which the provider passes on.
  await userEvent.click(box(subject), (shift ? { modifiers: ["Shift"] } : {}) as object);
}

// count returns the bar's count of n picked, "All n" when all is set.
function count(n: number, all = false) {
  return bar().getByText(`${all ? "All " : ""}${n}`, { exact: true });
}

// pickAll returns the box of the bar's checkbox named name: its input is
// hidden, under the box a pointer presses.
function pickAll(name: string): HTMLElement {
  const input = bar().getByRole("checkbox", { name }).element();
  const shown = input
    .closest("[data-slot=checkbox]")
    ?.querySelector<HTMLElement>("[data-slot=checkbox-box]");
  if (!shown) throw new Error(`No box for ${name}.`);
  return shown;
}

// bar returns the picking bar.
function bar() {
  return page.getByRole("toolbar", { name: "Picked conversations" });
}

describe("picking", () => {
  it("picks a row by its box, and with Shift every row up to another", async () => {
    await open([conversation(5), conversation(4), conversation(3), conversation(2)]);

    await pickBox("Subject 5");
    await expect.element(count(1)).toBeVisible();
    await pickBox("Subject 3", { shift: true });

    await expect.element(count(3)).toBeVisible();
    await expect.element(row("Subject 4")).toHaveAttribute("aria-selected", "true");
    await expect.element(row("Subject 2")).toHaveAttribute("aria-selected", "false");
  });

  it("opens a row pressed on its text while rows are picked, and keeps the picks", async () => {
    const rows = [conversation(5), conversation(4), conversation(3)];
    const router = await open(rows);
    await pickBox("Subject 5");

    await userEvent.click(row("Subject 3").getByText("Preview 3"));

    await expect.poll(() => router.state.location.pathname).toBe(`/mail/inbox/${rows[2].id}`);
    await expect.element(count(1)).toBeVisible();
    await expect.element(row("Subject 3")).toHaveAttribute("aria-selected", "false");
    await settled(router);
  });

  it("picks again, and Back stops it, while a conversation opened while picking stays open", async () => {
    const rows = Array.from({ length: 8 }, (_, n) => conversation(20 - n));
    const router = await open(rows);
    await pickBox("Subject 20");
    await pickBox("Subject 17", { shift: true });
    await userEvent.click(row("Subject 15").getByText("Preview 15"));
    await expect.poll(() => router.state.location.pathname).toBe(`/mail/inbox/${rows[5].id}`);
    await userEvent.click(bar().getByRole("button", { name: "Archive" }));
    await expect.element(bar()).not.toBeInTheDocument();
    // Picking's history entry is taken back before the next picking starts.
    await settled(router);
    await userEvent.click(
      page.getByRole("region", { name: "Undo" }).getByRole("button", { name: "Undo" }),
    );
    await expect.element(row("Subject 20")).toBeVisible();

    await pickBox("Subject 20");

    await expect.element(count(1)).toBeVisible();
    // Back stops this picking, though the conversation's entry is the last
    // picking's.
    router.history.back();
    await expect.element(bar()).not.toBeInTheDocument();
    expect(router.state.location.pathname).toBe(`/mail/inbox/${rows[5].id}`);
    await settled(router);
  });

  it("opens the focused row on Enter while rows are picked", async () => {
    const rows = [conversation(5), conversation(4)];
    const router = await open(rows);
    await pickBox("Subject 5");

    row("Subject 4").element().focus();
    await userEvent.keyboard("{Enter}");

    await expect.poll(() => router.state.location.pathname).toBe(`/mail/inbox/${rows[1].id}`);
    await expect.element(count(1)).toBeVisible();
    await settled(router);
  });

  it("picks with Space, and Escape stops picking", async () => {
    await open([conversation(5), conversation(4)]);

    row("Subject 4").element().focus();
    await userEvent.keyboard(" ");
    await expect.element(count(1)).toBeVisible();
    await userEvent.keyboard("{Escape}");

    await expect.element(bar()).not.toBeInTheDocument();
    await expect.element(page.getByRole("heading", { name: "Inbox" })).toBeVisible();
  });

  it("stops picking on Back, and takes back the entry it added when stopped another way", async () => {
    const router = await open([conversation(5), conversation(4)]);
    const entries = router.history.length;

    await pickBox("Subject 5");
    await expect.poll(() => router.history.length).toBe(entries + 1);
    router.history.back();
    await expect.element(bar()).not.toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/mail/inbox");

    await pickBox("Subject 4");
    await expect.poll(() => router.history.location.state.picking).toBeDefined();
    await pickBox("Subject 4");
    await expect.element(bar()).not.toBeInTheDocument();
    await expect.poll(() => router.history.location.state.picking).toBeUndefined();
  });

  it("archives the picked rows, with one Undo that puts them back unpicked", async () => {
    const rows = [conversation(5), conversation(4), conversation(3)];
    await open(rows);
    await pickBox("Subject 5");
    await pickBox("Subject 4");

    await userEvent.click(bar().getByRole("button", { name: "Archive" }));

    await expect.element(row("Subject 5")).not.toBeInTheDocument();
    await expect.element(bar()).not.toBeInTheDocument();
    // Focus goes to the row that took the first picked one's place.
    await expect.element(row("Subject 3")).toHaveFocus();
    await expect
      .element(page.getByText("Archived: 2 conversations. Find them in All mail."))
      .toBeInTheDocument();
    const undo = page.getByRole("region", { name: "Undo" });
    expect(api.actions).toEqual([{ action: "archive", ids: [rows[0].id, rows[1].id] }]);

    await userEvent.click(undo.getByRole("button", { name: "Undo" }));

    await expect.element(row("Subject 5")).toBeVisible();
    await expect.element(row("Subject 5")).toHaveAttribute("aria-selected", "false");
    await expect.element(bar()).not.toBeInTheDocument();
    // Picking starts again as before.
    await pickBox("Subject 4");
    await expect.element(count(1)).toBeVisible();
  });

  it("sends an action only to the picked rows it changes, which stay picked", async () => {
    const rows = [
      conversation(5, { unread: true }),
      conversation(4),
      conversation(3, { unread: true }),
    ];
    await open(rows);
    await pickBox("Subject 5");
    await pickBox("Subject 3", { shift: true });

    // Mark read while any is unread; Mark unread waits in More.
    await userEvent.click(bar().getByRole("button", { name: "Mark read" }));

    expect(api.actions).toEqual([{ action: "read", ids: [rows[0].id, rows[2].id] }]);
    await expect.element(count(3, true)).toBeVisible();
    await expect.element(page.getByText("Marked read: 2 conversations.")).toBeInTheDocument();
    await expect.element(bar().getByRole("button", { name: "Mark unread" })).toBeVisible();
  });

  it("sends an action only to picked rows whose account takes actions, the others staying picked", async () => {
    const rows = [conversation(5), conversation(4, { accountId: 2 })];
    await open(rows, {
      accounts: [
        listedAccount(1, "ana@example.com"),
        // Usable, as when no Google client is set up: only allowedActions says.
        { ...listedAccount(2, "bo@example.com"), allowedActions: ["rename", "disconnect"] },
      ],
    });
    await pickBox("Subject 5");
    await pickBox("Subject 4");

    await userEvent.click(bar().getByRole("button", { name: "Archive" }));

    expect(api.actions).toEqual([{ action: "archive", ids: [rows[0].id] }]);
    await expect.element(row("Subject 5")).not.toBeInTheDocument();
    await expect.element(row("Subject 4")).toHaveAttribute("aria-selected", "true");
    // Nothing is left that the bar can act on, so no More opens empty.
    await expect.element(bar().getByRole("button", { name: "Archive" })).not.toBeInTheDocument();
    expect(bar().getByRole("button", { name: "More actions" }).query()).toBeNull();
  });

  it("gives focus to the pick-all box when an action leaves the bar nothing to offer", async () => {
    const rows = [conversation(5), conversation(4, { accountId: 2 })];
    await open(rows, {
      accounts: [
        listedAccount(1, "ana@example.com"),
        { ...listedAccount(2, "bo@example.com"), allowedActions: ["rename", "disconnect"] },
      ],
    });
    await pickBox("Subject 5");
    await pickBox("Subject 4");

    bar().getByRole("button", { name: "Archive" }).element().focus();
    await userEvent.keyboard("{Enter}");

    await expect.element(row("Subject 5")).not.toBeInTheDocument();
    await expect.element(bar().getByRole("checkbox")).toHaveFocus();
  });

  it("reads the accounts again after a refusal to reconnect, and offers that account nothing", async () => {
    const rows = [conversation(5)];
    await open(rows);
    api.refuse[rows[0].id] = "needs-reconnect";
    // Google stops accepting the account as the action reaches the server,
    // after every read the page made before it.
    api.server.on("POST", "/api/v0/conversations/actions", (_, earlier) => {
      api.accounts = [
        {
          ...listedAccount(1, "ana@example.com"),
          status: "needs-reconnect",
          allowedActions: ["reconnect", "rename", "disconnect"],
        },
      ];
      return earlier();
    });
    await pickBox("Subject 5");

    await userEvent.click(bar().getByRole("button", { name: "Archive" }));

    await expect.element(row("Subject 5")).toHaveAttribute("aria-selected", "true");
    await expect.element(bar().getByRole("button", { name: "Archive" })).not.toBeInTheDocument();
    expect(row("Subject 5").getByRole("button", { name: "Star" }).query()).toBeNull();
  });

  it("keeps a refused row picked and says why in one toast, with details", async () => {
    const rows = [conversation(5), conversation(4), conversation(3)];
    await open(rows);
    api.refuse[rows[0].id] = "needs-reconnect";
    // Gone at Gmail, and from amplop's lists once its poll noticed.
    api.refuse[rows[1].id] = "not-found";
    api.conversations = [rows[0], rows[2]];
    await pickBox("Subject 5");
    await pickBox("Subject 3", { shift: true });

    await userEvent.click(bar().getByRole("button", { name: "Archive" }));

    await expect.element(page.getByText("Couldn't archive 2 conversations")).toBeVisible();
    await expect
      .element(
        page.getByText(
          "Reconnect ana@example.com in Connected accounts, then try again. 1 conversation is no longer in Gmail.",
        ),
      )
      .toBeVisible();
    await expect.element(row("Subject 5")).toHaveAttribute("aria-selected", "true");
    await expect.element(row("Subject 4")).not.toBeInTheDocument();
    // The one row left in the list.
    await expect.element(count(1, true)).toBeVisible();
    // The Undo is for the one that changed.
    await expect
      .element(page.getByText("Archived: Subject 3. Find it in All mail."))
      .toBeInTheDocument();

    await userEvent.click(page.getByRole("button", { name: "Show details" }));
    await expect.element(page.getByText("Account needs reconnecting")).toBeVisible();
    // One account: the lines name only their subjects.
    await expect
      .element(
        page
          .getByRole("list", { name: "Account needs reconnecting" })
          .getByText("Subject 5", { exact: true }),
      )
      .toBeVisible();
    await expect
      .element(
        page
          .getByRole("list", { name: "No longer in Gmail" })
          .getByText("Subject 4", { exact: true }),
      )
      .toBeVisible();
  });

  it("names each conversation's account in the details when they span several", async () => {
    const rows = [conversation(5), conversation(4, { accountId: 2 })];
    await open(rows, {
      accounts: [listedAccount(1, "ana@example.com"), listedAccount(2, "bo@example.com")],
    });
    api.refuse[rows[0].id] = "failed";
    api.refuse[rows[1].id] = "failed";
    await pickBox("Subject 5");
    await pickBox("Subject 4");

    await userEvent.click(bar().getByRole("button", { name: "Archive" }));
    await userEvent.click(page.getByRole("button", { name: "Show details" }));

    const lines = page.getByRole("list", { name: "Gmail didn't answer" });
    await expect.element(lines.getByText("Subject 5 ·\u00a0ana@example.com")).toBeVisible();
    await expect.element(lines.getByText("Subject 4 ·\u00a0bo@example.com")).toBeVisible();
  });

  it("closes a failure's toast once a newer action changes each conversation it named", async () => {
    const rows = [conversation(5), conversation(4)];
    await open(rows);
    api.refuse[rows[0].id] = "failed";
    await pickBox("Subject 5");
    await pickBox("Subject 4");
    await userEvent.click(bar().getByRole("button", { name: "Archive" }));
    await expect.element(page.getByText("Couldn't archive 1 conversation")).toBeVisible();

    delete api.refuse[rows[0].id];
    await userEvent.click(bar().getByRole("button", { name: "Archive" }));

    await expect.element(page.getByText("Couldn't archive 1 conversation")).not.toBeInTheDocument();
  });

  it("picks every loaded row from the bar's checkbox, all of them once the list has ended", async () => {
    await open([conversation(5), conversation(4), conversation(3)]);
    await pickBox("Subject 4");

    await userEvent.click(pickAll("Pick all 3 loaded conversations"));
    await expect.element(count(3, true)).toBeVisible();
    await userEvent.click(pickAll("Unpick all"));

    await expect.element(bar()).not.toBeInTheDocument();
  });

  it("picks only the loaded rows, and says how many, while older ones are still to load", async () => {
    await open(Array.from({ length: 60 }, (_, n) => conversation(n)));
    await pickBox("Subject 59");

    await userEvent.click(pickAll("Pick all 50 loaded conversations"));

    await expect.element(count(50)).toBeVisible();
  });

  it("sends more picked than the contract takes in requests of its limit, one after another", async () => {
    const clock = fakeClock();
    const rows = Array.from({ length: 120 }, (_, n) => conversation(n));
    await open(rows);
    // Each move to the last row loads the next page, until the end.
    const end = page.getByText("That's everything amplop has copied.", { exact: false });
    await vi.waitFor(
      async () => {
        const last = list()
          .getByRole("row", { name: /Subject \d+,/ })
          .last()
          .element();
        if (last instanceof HTMLElement) last.focus();
        await userEvent.keyboard("{End}");
        expect(end.query()).not.toBeNull();
      },
      { timeout: 10_000 },
    );
    const first = list().getByRole("row").first().element();
    if (first instanceof HTMLElement) first.focus();
    // React Aria's select all: Cmd+A on a Mac, Ctrl+A elsewhere.
    const mod = /Mac/.test(navigator.platform) ? "Meta" : "Control";
    await userEvent.keyboard(`{${mod}>}a{/${mod}}`);
    await expect.element(count(120, true)).toBeVisible();

    gated = true;
    await userEvent.click(bar().getByRole("button", { name: "Archive" }));
    await expect
      .element(page.getByText("Archived: 120 conversations. Find them in All mail."))
      .toBeInTheDocument();
    // Undo pressed at once waits for every answer, then undoes them all.
    await userEvent.click(
      page.getByRole("region", { name: "Undo" }).getByRole("button", { name: "Undo" }),
    );

    // One request at a time: each waits for the one before to answer.
    // oxlint-disable no-await-in-loop -- each step needs the one before it answered.
    for (let sent = 1; sent <= 5; sent++) {
      await expect.poll(() => waiting.length).toBe(1);
      // No second request while the first waits.
      await clock.advance(100);
      expect(waiting.length).toBe(1);
      waiting.shift()?.();
      await expect.poll(() => api.actions.length).toBe(sent);
    }
    // oxlint-enable no-await-in-loop
    expect(api.actions.map((a) => a.ids.length)).toEqual([25, 25, 25, 25, 20]);
    await expect
      .poll(() => api.requests.filter((r) => r === "/api/v0/conversations/changes").length, {
        timeout: 5000,
      })
      .toBe(5);
    await expect.element(row("Subject 119")).toBeVisible();
  });

  it("gives an action from the bar its Undo though it changes one picked row", async () => {
    const rows = [conversation(5, { unread: true }), conversation(4), conversation(3)];
    await open(rows);
    await pickBox("Subject 5");
    await pickBox("Subject 3", { shift: true });

    await userEvent.click(bar().getByRole("button", { name: "Mark read" }));

    expect(api.actions).toEqual([{ action: "read", ids: [rows[0].id] }]);
    await expect.element(page.getByText("Marked read: Subject 5.")).toBeInTheDocument();
  });

  it("keeps focus on a bar button whose action changes, e.g. Star to Remove star", async () => {
    await open([conversation(5), conversation(4)]);
    await pickBox("Subject 5");
    bar().getByRole("button", { name: "Star" }).element().focus();

    await userEvent.keyboard("{Enter}");

    await expect.element(bar().getByRole("button", { name: "Remove star" })).toHaveFocus();
  });

  it("stops picking on Escape from the bar, after its tooltip, focus back on the list's row", async () => {
    await open([conversation(5), conversation(4)]);
    row("Subject 4").element().focus();
    await userEvent.keyboard(" ");
    bar().getByRole("button", { name: "Archive" }).element().focus();
    // The first Escape closes the button's tooltip, which focus showed.
    await userEvent.keyboard("{Escape}");
    await expect.element(bar()).toBeVisible();

    await userEvent.keyboard("{Escape}");

    await expect.element(bar()).not.toBeInTheDocument();
    await expect.element(row("Subject 4")).toHaveFocus();
  });

  it("gives focus back to the list when the bar's checkbox unpicks every row", async () => {
    await open([conversation(5), conversation(4)]);
    row("Subject 5").element().focus();
    await userEvent.keyboard(" ");
    await userEvent.click(pickAll("Pick all 2 loaded conversations"));
    await expect.element(count(2, true)).toBeVisible();

    await userEvent.click(pickAll("Unpick all"));

    await expect.element(bar()).not.toBeInTheDocument();
    await expect.element(row("Subject 5")).toHaveFocus();
  });

  it("starts with none picked on coming back to a place", async () => {
    const router = await open([conversation(5), conversation(4, { inbox: false })]);
    await pickBox("Subject 5");
    await expect.element(count(1, true)).toBeVisible();

    await router.navigate({ to: "/mail/$place", params: { place: "all" } });
    await expect.element(row("Subject 4")).toBeVisible();
    await router.navigate({ to: "/mail/$place", params: { place: "inbox" } });

    await expect.element(row("Subject 5")).toHaveAttribute("aria-selected", "false");
    await expect.element(bar()).not.toBeInTheDocument();
  });

  it("puts focus on the list's row, not the bar's hidden heading, on coming back while picking", async () => {
    const rows = [conversation(5), conversation(4), conversation(3)];
    const router = await open(rows);
    await pickBox("Subject 5");
    await userEvent.click(row("Subject 3").getByText("Preview 3"));
    await expect.poll(() => router.state.location.pathname).toBe(`/mail/inbox/${rows[2].id}`);

    router.history.back();

    await expect.poll(() => router.state.location.pathname).toBe("/mail/inbox");
    await expect.element(row("Subject 3")).toHaveFocus();
    await expect.element(count(1)).toBeVisible();
  });

  it("is the top bar on a phone, after an arrow that stops picking", async () => {
    await open([conversation(5), conversation(4)], { width: 390 });
    await pickBox("Subject 5");

    await expect.element(bar()).toBeVisible();
    await expect
      .element(page.getByRole("button", { name: "Open sidebar" }))
      .not.toBeInTheDocument();
    await userEvent.click(bar().getByRole("button", { name: "Stop picking" }));

    await expect.element(bar()).not.toBeInTheDocument();
    await expect.element(page.getByRole("button", { name: "Open sidebar" })).toBeVisible();
  });

  it("takes a phone's top bar from an open search box, which stays closed after", async () => {
    await open([conversation(5), conversation(4)], { width: 390 });
    await userEvent.click(page.getByRole("button", { name: "Search", exact: true }));
    await expect.element(page.getByRole("button", { name: "Close search" })).toBeVisible();

    await pickBox("Subject 5");

    await expect.element(bar()).toBeVisible();
    await expect
      .element(page.getByRole("button", { name: "Close search" }))
      .not.toBeInTheDocument();
    await userEvent.click(bar().getByRole("button", { name: "Stop picking" }));
    await expect.element(page.getByRole("button", { name: "Search", exact: true })).toBeVisible();
    await expect.element(row("Subject 5")).toHaveFocus();
  });

  it("sits under the search box beside the list", async () => {
    const router = await open([conversation(5), conversation(4)]);
    await pickBox("Subject 5");

    await expect.element(bar()).toBeVisible();
    const search = page.getByRole("searchbox").element().getBoundingClientRect();
    expect(bar().element().getBoundingClientRect().top).toBeGreaterThanOrEqual(search.bottom);
    // Picking's history entry, added a moment after the pick.
    await expect.poll(() => router.history.location.state.picking).toBeDefined();
    await settled(router);
  });
});
