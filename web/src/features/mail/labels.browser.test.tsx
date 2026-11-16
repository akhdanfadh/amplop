import { RouterProvider } from "@tanstack/react-router";
import { afterEach, describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { cleanup, render } from "vitest-browser-react";
import type { ConversationRow, PlaceCount } from "@/api/generated/model";
import { closeToasts } from "@/components/ui/toast";
import { clearAccountCache } from "@/features/auth/session-cache";
import { appRouter, memberSession } from "@/test/app-router";
import { conversation, listedAccount, mailApi, type MailApi } from "@/test/mail-api";

// Labels' ids, as List Places gives them.
const clients = "00000000-0000-4000-8000-0000000000c1";
const acme = "00000000-0000-4000-8000-0000000000c2";
const hiring = "00000000-0000-4000-8000-0000000000c3";
const family = "00000000-0000-4000-8000-0000000000c4";

// label returns account's label place, named name under parent.
function label(accountId: number, place: string, name: string, parent: string | null = null) {
  return { accountId, place, name, parent, unread: 0 } satisfies PlaceCount;
}

const workLabels = [
  label(1, clients, "Clients"),
  label(1, acme, "Acme", clients),
  label(1, hiring, "Hiring"),
];

let api: MailApi;
let current: Awaited<ReturnType<typeof appRouter>> | undefined;

afterEach(async () => {
  closeToasts();
  await cleanup();
  if (current) clearAccountCache(current.options.context.queryClient);
  current = undefined;
});

// open renders the app at address over rows, of two accounts, Work's and
// Home's, with their labels, and returns its router once the reading pane
// shows.
async function open(rows: ConversationRow[], address: string) {
  await page.viewport(1440, 900);
  api = mailApi(rows, [listedAccount(1, "ana@work.example"), listedAccount(2, "ana@home.example")]);
  api.places = [...workLabels, label(2, family, "Family")];
  const router = await appRouter(address, memberSession, (url, init) => api.fetch(url, init));
  await render(<RouterProvider router={router} />);
  current = router;
  await expect.element(toolbar()).toBeVisible();
  return router;
}

// toolbar returns the reading pane's toolbar.
function toolbar() {
  return page.getByRole("toolbar", { name: "Conversation actions" });
}

// more opens the toolbar's More and the submenu named name in it.
async function more(name: string) {
  await userEvent.click(toolbar().getByRole("button", { name: "More actions" }));
  await userEvent.click(page.getByRole("menuitem", { name: `${name}…` }));
}

// pick presses the box of subject's row in the list, once the row has
// loaded.
async function pick(subject: string) {
  const row = page
    .getByRole("grid", { name: "Conversations" })
    .getByRole("row")
    .filter({ hasText: subject });
  await expect.element(row).toBeVisible();
  const box = row.element().querySelector<HTMLElement>("[data-slot=pick-box]");
  if (!box) throw new Error(`No box on ${subject}'s row.`);
  await userEvent.click(box);
}

describe("Move to", () => {
  it("adds the label, takes the conversation out of the list's place, and says where it went", async () => {
    const row = conversation(1, { accountId: 1 });
    const router = await open([row], `/mail/inbox/${row.id}`);

    await more("Move to");
    // Its title names it: More, which said what it was, has closed.
    await expect
      .element(page.getByRole("dialog", { name: "Move to" }).getByRole("heading"))
      .toHaveTextContent("Move to");
    // The labels nest under their parents, under the account's name, as
    // the unified Inbox shows every account's mail.
    await expect
      .element(page.getByRole("menu", { name: "Move to" }).getByText("ana@work.example"))
      .toBeVisible();
    await userEvent.click(page.getByRole("menuitem", { name: "Clients/Acme" }));

    await expect
      .poll(() => api.changes)
      .toEqual([[{ ids: [row.id], add: [acme], remove: ["inbox"] }]]);
    await expect.element(page.getByText("Moved to Clients/Acme", { exact: true })).toBeVisible();
    // Out of the Inbox, so back to the list.
    await expect.poll(() => router.state.location.pathname).toBe("/mail/inbox");
  });

  it("leaves out the label it moves from, and the list then shows it in the other at once", async () => {
    const row = conversation(1, { accountId: 1, inbox: false, labelIds: [hiring] });
    const router = await open([row], `/mail/${hiring}/${row.id}?account=1`);

    await more("Move to");
    await expect.element(page.getByRole("menuitem", { name: "Clients" })).toBeVisible();
    expect(page.getByRole("menuitem", { name: "Hiring" }).elements()).toHaveLength(0);
    await userEvent.click(page.getByRole("menuitem", { name: "Clients" }));
    await expect
      .poll(() => api.changes)
      .toEqual([[{ ids: [row.id], add: [clients], remove: [hiring] }]]);

    // Opened at once, the label's list shows the row, never "empty"
    // (ADR031, TanStack DB gate, rule 9).
    await router.navigate({
      to: "/mail/$place",
      params: { place: clients },
      search: { account: 1 },
    });
    await expect
      .element(page.getByRole("grid", { name: "Conversations" }).getByText(row.subject))
      .toBeVisible();
  });

  it("is not offered where nothing can leave, e.g. All mail", async () => {
    const row = conversation(1, { accountId: 1 });
    await open([row], `/mail/all/${row.id}`);

    await userEvent.click(toolbar().getByRole("button", { name: "More actions" }));
    await expect.element(page.getByRole("menuitem", { name: "Labels…" })).toBeVisible();
    expect(page.getByRole("menuitem", { name: "Move to…" }).elements()).toHaveLength(0);
  });
});

describe("Labels", () => {
  it("sends the ticked and unticked labels together on Apply, and keeps the conversation", async () => {
    const row = conversation(1, { accountId: 1, labelIds: [clients] });
    const router = await open([row], `/mail/inbox/${row.id}`);

    await more("Labels");
    await expect
      .element(page.getByRole("dialog", { name: "Labels" }).getByRole("heading"))
      .toHaveTextContent("Labels");
    const apply = page.getByRole("button", { name: "Apply" });
    await expect.element(apply).toBeDisabled();
    await userEvent.click(page.getByRole("menuitemcheckbox", { name: "Hiring" }));
    await userEvent.click(page.getByRole("menuitemcheckbox", { name: "Clients" }));
    await userEvent.click(apply);

    await expect
      .poll(() => api.changes)
      .toEqual([[{ ids: [row.id], add: [hiring], remove: [clients] }]]);
    await expect.element(page.getByText("Labels changed", { exact: true })).toBeVisible();
    expect(router.state.location.pathname).toBe(`/mail/inbox/${row.id}`);
    // The conversation stays, so focus goes back to More.
    await expect.element(toolbar().getByRole("button", { name: "More actions" })).toHaveFocus();
  });

  it("keeps the ticks through Escape, which clears the filter and then closes, sending nothing", async () => {
    const row = conversation(1, { accountId: 1, labelIds: [clients] });
    await open([row], `/mail/inbox/${row.id}`);

    await more("Labels");
    await userEvent.click(page.getByRole("menuitemcheckbox", { name: "Hiring" }));
    await userEvent.type(page.getByRole("searchbox", { name: "Filter labels" }), "Cli");
    // React Aria's menus clear a multiple selection on Escape by default.
    await userEvent.keyboard("{Escape}");
    await expect.element(page.getByRole("searchbox", { name: "Filter labels" })).toHaveValue("");
    await expect
      .element(page.getByRole("menuitemcheckbox", { name: "Clients", exact: true }))
      .toHaveAttribute("aria-checked", "true");
    await expect
      .element(page.getByRole("menuitemcheckbox", { name: "Hiring" }))
      .toHaveAttribute("aria-checked", "true");
    await userEvent.keyboard("{Escape}");

    await expect
      .element(page.getByRole("menuitemcheckbox", { name: "Hiring" }))
      .not.toBeInTheDocument();
    await expect.element(toolbar().getByRole("button", { name: "More actions" })).toHaveFocus();
    expect(api.changes).toEqual([]);
  });

  it("clears the filter from its button", async () => {
    const row = conversation(1, { accountId: 1 });
    await open([row], `/mail/inbox/${row.id}`);

    await more("Labels");
    const filter = page.getByRole("searchbox", { name: "Filter labels" });
    await userEvent.type(filter, "zzq");
    await expect.element(page.getByText("No labels match.")).toBeVisible();
    await userEvent.click(page.getByRole("button", { name: "Clear filter" }));

    await expect.element(filter).toHaveValue("");
    await expect.element(page.getByRole("menuitemcheckbox", { name: "Hiring" })).toBeVisible();
  });
});

describe("Labels at the contract's limit", () => {
  it("applies 100 labels added, and waits with words at 101", async () => {
    const row = conversation(1, { accountId: 1 });
    await page.viewport(1440, 900);
    api = mailApi([row], [listedAccount(1, "ana@work.example")]);
    // 101 labels, Label 001 to Label 101, as List Places gives them.
    api.places = Array.from({ length: 101 }, (_, i) => {
      const n = String(i + 1).padStart(3, "0");
      return label(1, `00000000-0000-4000-8000-000000001${n}`, `Label ${n}`);
    });
    const router = await appRouter(`/mail/inbox/${row.id}`, memberSession, (url, init) =>
      api.fetch(url, init),
    );
    await render(<RouterProvider router={router} />);
    current = router;
    await expect.element(toolbar()).toBeVisible();

    await more("Labels");
    const items = page.getByRole("menuitemcheckbox");
    await expect.element(items.nth(100)).toBeInTheDocument();
    for (let i = 0; i < 101; i++) {
      // One press after another, as a member ticks them.
      // oxlint-disable-next-line no-await-in-loop
      await userEvent.click(items.nth(i));
    }
    const apply = page.getByRole("button", { name: "Apply" });
    await expect.element(page.getByText("Add or remove up to 100 at once.")).toBeVisible();
    await expect.element(apply).toBeDisabled();
    await userEvent.click(items.nth(100));

    await expect.element(apply).toBeEnabled();
    expect(page.getByText("Add or remove up to 100 at once.").elements()).toHaveLength(0);
    // 102 presses, one after another: slow on a busy machine.
  }, 60_000);
});

describe("Labels from the keyboard", () => {
  it("ticks with Enter, keeps the menu open, and applies from Tab", async () => {
    const row = conversation(1, { accountId: 1 });
    await open([row], `/mail/inbox/${row.id}`);

    await more("Labels");
    await expect.element(page.getByRole("searchbox", { name: "Filter labels" })).toHaveFocus();
    // The first label, Clients, as the member sees the list.
    await userEvent.keyboard("{ArrowDown}");
    await userEvent.keyboard("{Enter}");
    await expect
      .element(page.getByRole("menuitemcheckbox", { name: "Clients", exact: true }))
      .toHaveAttribute("aria-checked", "true");
    await userEvent.keyboard("{Tab}");
    await expect.element(page.getByRole("button", { name: "Apply" })).toHaveFocus();
    await userEvent.keyboard("{Enter}");

    await expect.poll(() => api.changes).toEqual([[{ ids: [row.id], add: [clients], remove: [] }]]);
  });
});

describe("picked conversations of several accounts", () => {
  it("offers each account's labels under its name, says when one has none, and moves one account's", async () => {
    const work = conversation(3, { accountId: 1, subject: "Contract renewal" });
    const home = conversation(2, { accountId: 2, subject: "Internet bill" });
    const spare = conversation(1, { accountId: 3, subject: "Welcome aboard" });
    await page.viewport(1440, 900);
    api = mailApi(
      [work, home, spare],
      [
        listedAccount(1, "ana@work.example"),
        listedAccount(2, "ana@home.example"),
        listedAccount(3, "ana@spare.example"),
      ],
    );
    api.places = [...workLabels, label(2, family, "Family")];
    const router = await appRouter("/mail/inbox", memberSession, (url, init) =>
      api.fetch(url, init),
    );
    await render(<RouterProvider router={router} />);
    current = router;
    await pick("Contract renewal");
    await pick("Internet bill");
    await pick("Welcome aboard");

    await userEvent.click(
      page.getByRole("toolbar", { name: "Picked conversations" }).getByRole("button", {
        name: "More actions",
      }),
    );
    await userEvent.click(page.getByRole("menuitem", { name: "Move to…" }));
    const menu = page.getByRole("menu", { name: "Move to" });
    await expect.element(menu.getByText("ana@home.example")).toBeVisible();
    await expect
      .element(
        page.getByText("No labels in ana@spare.example yet. Labels you make in Gmail show here."),
      )
      .toBeVisible();
    await userEvent.click(page.getByRole("menuitem", { name: "Clients/Acme" }));

    await expect
      .poll(() => api.changes)
      .toEqual([[{ ids: [work.id], add: [acme], remove: ["inbox"] }]]);
  });
});
