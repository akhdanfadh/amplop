import "@/zod-config";
import { RouterProvider } from "@tanstack/react-router";
import { afterEach, describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { cleanup, render } from "vitest-browser-react";
import type { ConversationRow, ListedAccount, PlaceCount } from "@/api/generated/model";
import { closeToasts } from "@/components/ui/toast";
import { clearAccountCache } from "@/features/auth/session-cache";
import { appRouter, memberSession } from "@/test/app-router";
import { listedAccount, mailApi, type MailApi } from "@/test/mail-api";

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
