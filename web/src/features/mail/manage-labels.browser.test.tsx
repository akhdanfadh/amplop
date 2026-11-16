import "@/zod-config";
import { RouterProvider } from "@tanstack/react-router";
import { afterEach, describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { cleanup, render } from "vitest-browser-react";
import type { ConversationRow, ListedAccount, PlaceCount } from "@/api/generated/model";
import { closeToasts } from "@/components/ui/toast";
import { clearAccountCache } from "@/features/auth/session-cache";
import { appRouter, memberSession } from "@/test/app-router";
import { conversation, listedAccount, mailApi, type MailApi } from "@/test/mail-api";

// Labels' ids, as List Places gives them.
const clients = "00000000-0000-4000-8000-0000000000d1";
const acme = "00000000-0000-4000-8000-0000000000d2";
const hiring = "00000000-0000-4000-8000-0000000000d3";
const family = "00000000-0000-4000-8000-0000000000d4";

// label returns account's label place, named name under parent.
function label(accountId: number, place: string, name: string, parent: string | null = null) {
  return { accountId, place, name, parent, unread: 0 } satisfies PlaceCount;
}

// The places of Work, account 1, and Home, account 2: their Inboxes, and
// Work's Clients holding Acme, and Hiring; Home's Family.
function places(): PlaceCount[] {
  return [
    { accountId: 1, place: "inbox", name: null, parent: null, unread: 0 },
    { accountId: 2, place: "inbox", name: null, parent: null, unread: 0 },
    label(1, clients, "Clients"),
    label(1, acme, "Acme", clients),
    label(1, hiring, "Hiring"),
    label(2, family, "Family"),
  ];
}

// accounts returns Work and Home, both able to change their mail.
function accounts(): ListedAccount[] {
  return [
    { ...listedAccount(1, "ana@work.example"), name: "Work" },
    { ...listedAccount(2, "ana@home.example"), name: "Home" },
  ];
}

let api: MailApi;
let current: Awaited<ReturnType<typeof appRouter>> | undefined;

afterEach(async () => {
  closeToasts();
  await cleanup();
  if (current) clearAccountCache(current.options.context.queryClient);
  current = undefined;
});

// open renders the app at address over rows, with accounts and places,
// and returns its router.
async function open(address: string, rows: ConversationRow[] = [], listed = accounts()) {
  await page.viewport(1440, 900);
  api = mailApi(rows, listed);
  api.places = places();
  const router = await appRouter(address, memberSession, (url, init) => api.fetch(url, init));
  await render(<RouterProvider router={router} />);
  current = router;
  return router;
}

// nav returns the mail sidebar.
function nav() {
  return page.getByRole("navigation", { name: "Mail" });
}

// placeOf returns the id of the label of account named name.
function placeOf(accountId: number, name: string): string | undefined {
  return api.places.find((p) => p.accountId === accountId && p.name === name)?.place;
}

// toolbar returns the reading pane's toolbar.
function toolbar() {
  return page.getByRole("toolbar", { name: "Conversation actions" });
}

// picker opens the toolbar's More and the picker named name in it.
async function picker(name: string) {
  await userEvent.click(toolbar().getByRole("button", { name: "More actions" }));
  await userEvent.click(page.getByRole("menuitem", { name: `${name}…` }));
}

describe("New label in the sidebar", () => {
  it("makes a label in the account chosen, nested where chosen, and shows it", async () => {
    await open("/mail/inbox?account=2");

    await userEvent.click(nav().getByRole("button", { name: "New label" }));
    const dialog = page.getByRole("dialog", { name: "New label" });
    await expect.element(dialog.getByRole("textbox", { name: "Name" })).toHaveFocus();
    // The account the sidebar shows, Home; Work is the other choice.
    await expect.element(dialog.getByRole("button", { name: /Account/ })).toHaveTextContent("Home");
    await userEvent.click(dialog.getByRole("button", { name: /Account/ }));
    await userEvent.click(page.getByRole("option", { name: "Work" }));
    await userEvent.fill(dialog.getByRole("textbox", { name: "Name" }), "Initech");
    await userEvent.click(dialog.getByRole("button", { name: /Nest under/ }));
    // Work's labels, in the sidebar's order, Home's not.
    await expect.element(page.getByRole("option", { name: "Clients/Acme" })).toBeVisible();
    expect(page.getByRole("option", { name: "Family" }).elements()).toHaveLength(0);
    await userEvent.click(page.getByRole("option", { name: "Clients" }));
    await userEvent.click(dialog.getByRole("button", { name: "Create" }));

    await expect.element(dialog).not.toBeInTheDocument();
    expect(api.labelWrites).toEqual(["POST /api/v0/labels"]);
    expect(api.places.find((p) => p.name === "Initech")).toMatchObject({
      accountId: 1,
      parent: clients,
    });
    // Its way unfolds, so it shows where it went.
    await expect.element(nav().getByRole("link", { name: "Initech" })).toBeVisible();
  });

  it("names the account in its title when it has one, and shows the server's refusal at the name", async () => {
    await open("/mail/inbox", [], [accounts()[0]]);
    api.refuseLabel = "Gmail keeps this name for itself.";

    await userEvent.click(nav().getByRole("button", { name: "New label" }));
    const dialog = page.getByRole("dialog", { name: "New label in Work" });
    expect(dialog.getByRole("button", { name: /Account/ }).elements()).toHaveLength(0);
    await userEvent.fill(dialog.getByRole("textbox", { name: "Name" }), "Inbox");
    await userEvent.click(dialog.getByRole("button", { name: "Create" }));

    await expect
      .element(dialog.getByRole("textbox", { name: "Name" }))
      .toHaveAccessibleDescription("Gmail keeps this name for itself.");
    // Escape sends nothing more and closes it.
    await userEvent.keyboard("{Escape}");
    await expect.element(dialog).not.toBeInTheDocument();
    expect(api.labelWrites).toEqual(["POST /api/v0/labels"]);
  });

  it("is offered only for accounts whose mail can change", async () => {
    const work = accounts()[0];
    const home = {
      ...accounts()[1],
      allowedActions: accounts()[1].allowedActions.filter((a) => a !== "change-mail"),
    };
    await open("/mail/inbox", [], [work, home]);

    // Only Work takes a new one, so the dialog names it, with no Account.
    await userEvent.click(nav().getByRole("button", { name: "New label" }));
    const dialog = page.getByRole("dialog", { name: "New label in Work" });
    await expect.element(dialog).toBeVisible();
    expect(dialog.getByRole("button", { name: /Account/ }).elements()).toHaveLength(0);
  });

  it("is not offered when no account's mail can change", async () => {
    const stuck = accounts();
    for (const a of stuck) {
      a.allowedActions = a.allowedActions.filter((action) => action !== "change-mail");
    }
    await open("/mail/inbox", [], stuck);

    await expect.element(nav().getByRole("link", { name: "Work" })).toBeVisible();
    expect(nav().getByRole("button", { name: "New label" }).elements()).toHaveLength(0);
  });
});

describe("New label in a picker", () => {
  it("in Labels, opens the dialog split at the label typed, and brings the label back ticked", async () => {
    const row = conversation(1, { accountId: 1 });
    await open(`/mail/inbox/${row.id}`, [row]);
    await expect.element(toolbar()).toBeVisible();

    await picker("Labels");
    await userEvent.fill(page.getByRole("searchbox", { name: "Filter labels" }), "clients/Initech");
    await userEvent.click(page.getByRole("menuitem", { name: 'New label "clients/Initech"…' }));
    const dialog = page.getByRole("dialog", { name: "New label in Work" });
    await expect.element(dialog.getByRole("textbox", { name: "Name" })).toHaveValue("Initech");
    await expect
      .element(dialog.getByRole("button", { name: /Nest under/ }))
      .toHaveTextContent("Clients");
    await userEvent.click(dialog.getByRole("button", { name: "Create" }));
    await expect.element(dialog).not.toBeInTheDocument();

    const initech = placeOf(1, "Initech");
    await expect
      .element(page.getByRole("menuitemcheckbox", { name: "Clients/Initech" }))
      .toBeChecked();
    // Under its parent, in name order, as the sidebar's tree has it.
    await userEvent.click(page.getByRole("button", { name: "Clear filter" }));
    await expect
      .poll(() =>
        page
          .getByRole("menuitemcheckbox")
          .elements()
          .map((item) => item.getAttribute("aria-label")),
      )
      .toEqual(["Clients", "Clients/Acme", "Clients/Initech", "Hiring"]);
    await userEvent.click(page.getByRole("button", { name: "Apply" }));
    await expect.poll(() => api.changes).toEqual([[{ ids: [row.id], add: [initech], remove: [] }]]);
  });

  it("in Move to, makes the label and moves there, and Escape closes only the dialog", async () => {
    const row = conversation(1, { accountId: 1 });
    await open(`/mail/inbox/${row.id}`, [row]);
    await expect.element(toolbar()).toBeVisible();

    await picker("Move to");
    await userEvent.fill(page.getByRole("searchbox", { name: "Filter labels" }), "Taxes");
    await userEvent.click(page.getByRole("menuitem", { name: 'New label "Taxes"…' }));
    const dialog = page.getByRole("dialog", { name: "New label in Work" });
    await userEvent.keyboard("{Escape}");
    await expect.element(dialog).not.toBeInTheDocument();
    await expect.element(page.getByRole("dialog", { name: "Move to" })).toBeVisible();

    await userEvent.click(page.getByRole("menuitem", { name: 'New label "Taxes"…' }));
    await userEvent.click(dialog.getByRole("button", { name: "Create and move" }));

    const taxes = placeOf(1, "Taxes");
    await expect
      .poll(() => api.changes)
      .toEqual([[{ ids: [row.id], add: [taxes], remove: ["inbox"] }]]);
    await expect.element(page.getByText("Moved to Taxes", { exact: true })).toBeVisible();
  });

  it("isn't offered where the account's labels can't change, which says no label matches", async () => {
    const row = conversation(1, { accountId: 1 });
    const [work] = accounts();
    work.allowedActions = work.allowedActions.filter((action) => action !== "change-mail");
    await open(`/mail/inbox/${row.id}`, [row], [work]);
    await expect.element(toolbar()).toBeVisible();

    await picker("Labels");
    await userEvent.fill(page.getByRole("searchbox", { name: "Filter labels" }), "zzq");
    await expect.element(page.getByText("No labels match.")).toBeVisible();
    expect(page.getByRole("menuitem", { name: /New label/ }).elements()).toHaveLength(0);
  });

  it("isn't offered for a name a label shown has, in any case", async () => {
    const row = conversation(1, { accountId: 1 });
    await open(`/mail/inbox/${row.id}`, [row]);
    await expect.element(toolbar()).toBeVisible();

    await picker("Labels");
    await userEvent.fill(page.getByRole("searchbox", { name: "Filter labels" }), "hiring");
    await expect.element(page.getByRole("menuitemcheckbox", { name: "Hiring" })).toBeVisible();
    expect(page.getByRole("menuitem", { name: /New label/ }).elements()).toHaveLength(0);
  });
});
