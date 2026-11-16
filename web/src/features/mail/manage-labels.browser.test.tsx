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
import { refreshMail } from "./conversations";

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

// labelAction opens the sidebar label at path's menu and chooses item.
async function labelAction(path: string, item: string) {
  await userEvent.click(nav().getByRole("button", { name: `Actions for ${path}` }));
  await userEvent.click(page.getByRole("menuitem", { name: item }));
}

// confirm presses the button named name in dialog from the keyboard: a
// pointer's press just after the dialog opens is taken for a double
// click's second, which the dialog ignores (AlertDialogAction).
async function confirm(dialog: ReturnType<typeof page.getByRole>, name: string) {
  dialog.getByRole("button", { name }).element().focus();
  await userEvent.keyboard("{Enter}");
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

    await userEvent.click(nav().getByRole("button", { name: "Labels of Home" }));
    await expect.element(nav().getByRole("link", { name: "Family" })).toBeVisible();
    expect(nav().getByRole("button", { name: "Actions for Family" }).elements()).toHaveLength(0);
    await userEvent.click(nav().getByRole("button", { name: "Labels of Work" }));
    await expect.element(nav().getByRole("button", { name: "Actions for Hiring" })).toBeVisible();
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

describe("a sidebar label's Edit", () => {
  it("renames the open label, and its page follows", async () => {
    await open(`/mail/${clients}?account=1`);
    await expect.element(page.getByRole("heading", { name: "Clients", level: 1 })).toBeVisible();

    await labelAction("Clients", "Edit…");
    const dialog = page.getByRole("dialog", { name: "Edit label" });
    const name = dialog.getByRole("textbox", { name: "Name" });
    await expect.element(name).toHaveValue("Clients");
    await expect.element(name).toHaveFocus();
    // It can't nest in itself or a label inside it.
    await userEvent.click(dialog.getByRole("button", { name: /Nest under/ }));
    await expect.element(page.getByRole("option", { name: "Hiring" })).toBeVisible();
    expect(page.getByRole("option", { name: "Clients/Acme" }).elements()).toHaveLength(0);
    await userEvent.keyboard("{Escape}");
    await userEvent.fill(name, "Customers");
    await userEvent.click(dialog.getByRole("button", { name: "Save" }));

    await expect.element(dialog).not.toBeInTheDocument();
    expect(api.labelWrites).toEqual([`PATCH /api/v0/labels/${clients}`]);
    await expect.element(page.getByRole("heading", { name: "Customers", level: 1 })).toBeVisible();
    await expect.element(nav().getByRole("link", { name: "Customers" })).toBeVisible();
  });
});

describe("a sidebar label's Delete", () => {
  it("asks, listing the labels inside, then leaves the open label for its account's Inbox", async () => {
    const row = conversation(1, { accountId: 1, labelIds: [acme] });
    const router = await open(`/mail/${acme}?account=1`, [row]);

    await labelAction("Clients", "Delete label…");
    const dialog = page.getByRole("alertdialog", { name: "Delete Clients?" });
    await expect
      .element(dialog)
      .toHaveAccessibleDescription(
        "Clients and the label inside it are deleted in Gmail too, and this can't be undone. Your mail stays: their conversations keep their other labels and stay in All mail.",
      );
    await expect
      .element(dialog.getByRole("list", { name: "Also deleted" }))
      .toHaveTextContent("Acme");
    await confirm(dialog, "Delete Clients");

    await expect.element(dialog).not.toBeInTheDocument();
    expect(api.labelWrites).toEqual([`DELETE /api/v0/labels/${clients}`]);
    expect(router.state.location.href).toBe("/mail/inbox?account=1");
    expect(nav().getByRole("link", { name: "Clients" }).elements()).toHaveLength(0);
    // Screen readers hear it, from the Undo bar's live region.
    await expect.element(page.getByText("Clients deleted.", { exact: true })).toBeInTheDocument();
  });

  it("leaves the open label once a delete that then failed took it", async () => {
    const router = await open(`/mail/${acme}?account=1`);
    api.deleteOnly = 1;

    await labelAction("Clients", "Delete label…");
    const dialog = page.getByRole("alertdialog", { name: "Delete Clients?" });
    await confirm(dialog, "Delete Clients");

    // Acme went before Gmail stopped: its page gives way to the Inbox.
    await expect
      .element(dialog.getByRole("alert"))
      .toHaveTextContent("Some labels weren't deleted. Delete again to finish.");
    expect(router.state.location.href).toBe("/mail/inbox?account=1");
  });

  it("says a delete stopped part way, and finishes it when pressed again", async () => {
    await open("/mail/inbox?account=1");
    api.deleteOnly = 1;

    // Work's labels show, as the filter's account's do.
    await labelAction("Clients", "Delete label…");
    const dialog = page.getByRole("alertdialog", { name: "Delete Clients?" });
    await confirm(dialog, "Delete Clients");

    await expect
      .element(dialog.getByRole("alert"))
      .toHaveTextContent("Some labels weren't deleted. Delete again to finish.");
    // Acme went: the list and the words say what is left.
    expect(dialog.getByRole("list", { name: "Also deleted" }).elements()).toHaveLength(0);
    await confirm(dialog, "Delete Clients");
    await expect.element(dialog).not.toBeInTheDocument();
    expect(placeOf(1, "Clients")).toBeUndefined();
  });
});

describe("a refused edit", () => {
  it("still reads the labels again, as Gmail may have renamed some", async () => {
    await open(`/mail/inbox?account=1`);
    // Gmail renamed Clients, then failed on a label inside it.
    api.refuseLabel = 503;
    const renamed = api.places.map((p) => (p.place === clients ? { ...p, name: "Customers" } : p));

    await labelAction("Clients", "Edit…");
    const dialog = page.getByRole("dialog", { name: "Edit label" });
    await userEvent.fill(dialog.getByRole("textbox", { name: "Name" }), "Customers");
    api.places = renamed;
    await userEvent.click(dialog.getByRole("button", { name: "Save" }));

    await expect.element(dialog.getByRole("alert")).toBeVisible();
    await expect.element(nav().getByRole("link", { name: "Customers" })).toBeVisible();
  });
});

describe("the open label", () => {
  it("follows a rename a sync finds", async () => {
    const router = await open(`/mail/${hiring}?account=1`);
    await expect.element(page.getByRole("heading", { name: "Hiring", level: 1 })).toBeVisible();

    api.places = api.places.map((p) => (p.place === hiring ? { ...p, name: "Recruiting" } : p));
    await refreshMail(router.options.context.queryClient);

    await expect.element(page.getByRole("heading", { name: "Recruiting", level: 1 })).toBeVisible();
  });
});

describe("a label gone", () => {
  it("says so at an old link, and Page not found stays for an address that is no place", async () => {
    await open("/mail/00000000-0000-4000-8000-0000000000ff?account=1");
    await expect
      .element(page.getByRole("heading", { name: "This label doesn't exist" }))
      .toBeVisible();
    await expect
      .element(page.getByRole("link", { name: "Open Inbox" }))
      .toHaveAttribute("href", "/mail/inbox");
  });

  it("says so once a sync finds the open label deleted in Gmail", async () => {
    const router = await open(`/mail/${hiring}?account=1`);
    await expect.element(page.getByRole("heading", { name: "Hiring", level: 1 })).toBeVisible();

    api.places = api.places.filter((p) => p.place !== hiring);
    await refreshMail(router.options.context.queryClient);

    await expect
      .element(page.getByRole("heading", { name: "This label doesn't exist" }))
      .toBeVisible();
  });

  it("says so once the last account goes, leaving no places at all", async () => {
    const router = await open(`/mail/${hiring}?account=1`);
    await expect.element(page.getByRole("heading", { name: "Hiring", level: 1 })).toBeVisible();

    api.places = [];
    await refreshMail(router.options.context.queryClient);

    await expect
      .element(page.getByRole("heading", { name: "This label doesn't exist" }))
      .toBeVisible();
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

  it("isn't offered where the account's mail can't change, whose toolbar opens no picker", async () => {
    const row = conversation(1, { accountId: 1 });
    const [work] = accounts();
    work.allowedActions = work.allowedActions.filter((action) => action !== "change-mail");
    await open(`/mail/inbox/${row.id}`, [row], [work]);
    await expect.element(page.getByRole("heading", { name: row.subject })).toBeVisible();

    // No More at all: it would hold only Move to and Labels, and the
    // conversation has no Gmail link here.
    expect(page.getByRole("button", { name: "More actions" }).elements()).toHaveLength(0);
    expect(page.getByRole("button", { name: "Archive" }).elements()).toHaveLength(0);
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
