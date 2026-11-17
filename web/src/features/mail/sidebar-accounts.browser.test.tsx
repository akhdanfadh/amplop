import { RouterProvider } from "@tanstack/react-router";
import { afterEach, describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { cleanup, render } from "vitest-browser-react";
import { clearAccountCache } from "@/features/auth/session-cache";
import type { ListedAccount, PlaceCount } from "@/api/generated/model";
import { appRouter } from "@/test/app-router";
import { conversation, listedAccount, mailApi, type MailApi } from "@/test/mail-api";

const work = "11111111-1111-4111-8111-111111111111";
const projects = "22222222-2222-4222-8222-222222222222";

// accounts returns three accounts: Personal, named, up to date; a work
// address copying its mail; and one Google stopped accepting.
function accounts(): ListedAccount[] {
  const personal = { ...listedAccount(1, "ana@example.com"), name: "Personal" };
  const office = listedAccount(2, "ana@work.example");
  office.sync = { ...office.sync, status: "running", messagesSoFar: 1200, lastSyncedAt: null };
  const old = {
    ...listedAccount(3, "ana@old.example"),
    status: "needs-reconnect" as const,
    needsReconnectSince: "2026-11-12T08:00:00Z",
    allowedActions: ["reconnect" as const, "rename" as const, "disconnect" as const],
  };
  return [personal, office, old];
}

// places returns the accounts' Inbox counts and Personal's labels: Work,
// and Projects under it.
function places(): PlaceCount[] {
  return [
    { accountId: 1, place: "inbox", name: null, parent: null, unread: 3 },
    { accountId: 2, place: "inbox", name: null, parent: null, unread: 2 },
    { accountId: 3, place: "inbox", name: null, parent: null, unread: 0 },
    { accountId: 1, place: work, name: "Work", parent: null, unread: 0 },
    { accountId: 1, place: projects, name: "Projects", parent: work, unread: 4 },
  ];
}

let api: MailApi;
let current: Awaited<ReturnType<typeof appRouter>> | undefined;

// open renders the app at address on a window width wide, with accounts
// and places; prepare changes the fake API before the app reads it.
async function open(address: string, width = 1440, prepare: (api: MailApi) => void = () => {}) {
  await page.viewport(width, 900);
  api = mailApi([conversation(1)], accounts());
  api.places = places();
  prepare(api);
  const router = await appRouter(address, api.server);
  await render(<RouterProvider router={router} />);
  current = router;
  return router;
}

// nav returns the mail sidebar.
function nav() {
  return page.getByRole("navigation", { name: "Mail" });
}

afterEach(async () => {
  await cleanup();
  // The member's data ends, folds included, so each test starts folded.
  if (current) clearAccountCache(current.options.context.queryClient);
  current = undefined;
});

// labelsOf returns the chevron that folds or unfolds the labels of the
// account called name, and labelsUnder those under the label at path.
function labelsOf(name: string) {
  return nav().getByRole("button", { name: `Labels of ${name}` });
}
function labelsUnder(path: string) {
  return nav().getByRole("button", { name: `Labels under ${path}` });
}

// reads returns how many times the app has read path.
function reads(path: string): number {
  return api.requests.filter((request) => request === path).length;
}

describe("the sidebar's accounts", () => {
  it("lists each account with its inbox's unread count, its status, and its labels", async () => {
    await open("/mail/inbox");

    await expect.element(nav().getByRole("link", { name: "All accounts" })).toBeVisible();
    await expect.element(nav().getByRole("link", { name: "Personal 3 unread" })).toBeVisible();
    await expect
      .element(nav().getByRole("link", { name: "ana@work.example Syncing 2 unread" }))
      .toBeVisible();
    await expect
      .element(nav().getByRole("link", { name: "ana@old.example Reconnect" }))
      .toBeVisible();
    // Every account's Inbox counts.
    await expect.element(nav().getByRole("link", { name: "Inbox 5 unread" })).toBeVisible();
    // Projects nests under Work, by its own name, both folded at first.
    expect(nav().getByRole("link", { name: "Projects 4 unread" }).elements()).toHaveLength(0);
    await userEvent.click(labelsOf("Personal"));
    await userEvent.click(labelsUnder("Work"));
    const projectsLink = nav().getByRole("link", { name: "Projects 4 unread" });
    // A label opens in its account.
    await expect.element(projectsLink).toHaveAttribute("href", `/mail/${projects}?account=1`);
  });

  it("marks the filtering account as current, and the open place as the page", async () => {
    await open("/mail/inbox?account=1");

    await expect
      .element(nav().getByRole("link", { name: "Personal 3 unread" }))
      .toHaveAttribute("aria-current", "true");
    await expect
      .element(nav().getByRole("link", { name: "Inbox 3 unread" }))
      .toHaveAttribute("aria-current", "page");
  });

  it("reads the places once for a label's page and its sidebar", async () => {
    await open(`/mail/${projects}`);

    await expect.element(nav().getByRole("link", { name: "Projects 4 unread" })).toBeVisible();
    expect(reads("/api/v0/places")).toBe(1);
  });

  it("says when the accounts could not be read, and reads them again", async () => {
    await open("/mail/inbox", 1440, (fake) => {
      fake.failAccounts = 500;
    });

    await expect
      .element(nav().getByRole("alert"))
      .toHaveTextContent("Couldn't load your accounts.");
    api.failAccounts = undefined;
    await userEvent.click(nav().getByRole("button", { name: "Try again" }));

    await expect.element(nav().getByRole("link", { name: "Personal 3 unread" })).toBeVisible();
  });

  it("says so on the rail too, with one icon that reads them again", async () => {
    await open("/mail/inbox", 1100, (fake) => {
      fake.failAccounts = 500;
    });

    await expect
      .element(nav().getByRole("alert"))
      .toHaveTextContent("Couldn't load your accounts.");
    // The rail's one icon, not the open sidebar's button.
    const retry = nav().getByRole("button", { name: "Try again" });
    await expect.element(retry).toHaveAttribute("data-slot", "sidebar-menu-button");
    api.failAccounts = undefined;
    await userEvent.click(retry);

    await expect.element(nav().getByRole("link", { name: /^Personal/ })).toBeInTheDocument();
  });

  it("filters every view to the chosen account, and All accounts ends it", async () => {
    const router = await open("/mail/inbox");

    await userEvent.click(nav().getByRole("link", { name: /^Personal/ }));
    await expect.poll(() => router.state.location.search).toEqual({ account: 1 });
    await expect.element(nav().getByRole("link", { name: "Inbox 3 unread" })).toBeVisible();

    await userEvent.click(nav().getByRole("link", { name: "Starred" }));
    await expect.poll(() => router.state.location.pathname).toBe("/mail/starred");
    expect(router.state.location.search).toEqual({ account: 1 });

    await userEvent.click(nav().getByRole("link", { name: "All accounts" }));
    await expect.poll(() => router.state.location.search).toEqual({});
    expect(router.state.location.pathname).toBe("/mail/starred");
  });

  it("opens a label in its own account, from every account's mail or another's", async () => {
    const router = await open("/mail/starred?account=2");
    await userEvent.click(labelsOf("Personal"));

    await userEvent.click(nav().getByRole("link", { name: "Work" }));

    await expect.poll(() => router.state.location.search).toEqual({ account: 1 });
    await expect
      .element(nav().getByRole("link", { name: /^Personal/ }))
      .toHaveAttribute("aria-current", "true");
    await expect
      .element(nav().getByRole("link", { name: /^ana@work\.example/ }))
      .not.toHaveAttribute("aria-current");
    await expect
      .element(nav().getByRole("link", { name: "All accounts" }))
      .not.toHaveAttribute("aria-current");
    // Every account's mail has no labels: All accounts opens its Inbox.
    await userEvent.click(nav().getByRole("link", { name: "All accounts" }));
    await expect.poll(() => router.state.location.search).toEqual({});
    expect(router.state.location.pathname).toBe("/mail/inbox");
  });

  it("opens a label from every account's mail in the label's account", async () => {
    const router = await open("/mail/inbox");
    await userEvent.click(labelsOf("Personal"));

    await userEvent.click(nav().getByRole("link", { name: "Work" }));

    await expect.poll(() => router.state.location.search).toEqual({ account: 1 });
    await expect
      .element(nav().getByRole("link", { name: "All accounts" }))
      .not.toHaveAttribute("aria-current");
  });

  it("opens an old link to a label in the label's account", async () => {
    const router = await open(`/mail/${projects}`);

    await expect.poll(() => router.state.location.search).toEqual({ account: 1 });
    await expect.element(page.getByRole("main").getByText("Personal")).toBeVisible();
  });

  it("opens a nested label named with its parent, and another account's Inbox from it", async () => {
    const router = await open("/mail/inbox");
    await userEvent.click(labelsOf("Personal"));
    await userEvent.click(labelsUnder("Work"));

    await userEvent.click(nav().getByRole("link", { name: /^Projects/ }));
    await expect
      .element(page.getByRole("heading", { level: 1, name: "Work/Projects" }))
      .toBeVisible();

    await userEvent.click(nav().getByRole("link", { name: /ana@work\.example/ }));
    await expect.poll(() => router.state.location.pathname).toBe("/mail/inbox");
    expect(router.state.location.search).toEqual({ account: 2 });
  });
});

describe("folding labels", () => {
  it("keeps what the member unfolds and folds for the next visit", async () => {
    await open("/mail/inbox");
    await expect.element(labelsOf("Personal")).toHaveAttribute("aria-expanded", "false");

    await userEvent.click(labelsOf("Personal"));
    await expect.element(nav().getByRole("link", { name: "Work" })).toBeVisible();
    await expect.element(labelsUnder("Work")).toHaveAttribute("aria-expanded", "false");
    await cleanup();
    await open("/mail/inbox");

    await expect.element(labelsOf("Personal")).toHaveAttribute("aria-expanded", "true");
    await userEvent.click(labelsOf("Personal"));
    expect(nav().getByRole("link", { name: "Work" }).elements()).toHaveLength(0);
  });

  it("unfolds the way to the open label, and the filtered account", async () => {
    await open(`/mail/${projects}`);

    await expect
      .element(nav().getByRole("link", { name: "Projects 4 unread" }))
      .toHaveAttribute("aria-current", "page");
    await expect.element(labelsUnder("Work")).toHaveAttribute("aria-expanded", "true");
    await cleanup();
    await open("/mail/inbox?account=1");
    await expect.element(labelsOf("Personal")).toHaveAttribute("aria-expanded", "true");
  });

  it("folds everything again for the next member", async () => {
    const router = await open("/mail/inbox");
    await userEvent.click(labelsOf("Personal"));
    await expect.element(labelsOf("Personal")).toHaveAttribute("aria-expanded", "true");
    await cleanup();
    clearAccountCache(router.options.context.queryClient);

    await open("/mail/inbox");
    await expect.element(labelsOf("Personal")).toHaveAttribute("aria-expanded", "false");
  });

  it("formats a large count with a comma", async () => {
    await open("/mail/inbox", 1440, (fake) => {
      fake.places[0] = { ...fake.places[0], unread: 12847 };
    });

    await expect.element(nav().getByRole("link", { name: "Personal 12,847 unread" })).toBeVisible();
    await expect.element(nav().getByRole("link", { name: "Inbox 12,849 unread" })).toBeVisible();
  });
});

describe("Check for new mail", () => {
  it("checks every account the list shows that inbok can use", async () => {
    await open("/mail/inbox");

    await userEvent.click(page.getByRole("button", { name: "Check for new mail" }));

    await expect.poll(() => new Set(api.syncs)).toEqual(new Set([1, 2]));
  });

  it("checks only the account the list is filtered to", async () => {
    await open("/mail/inbox?account=2");

    await userEvent.click(page.getByRole("button", { name: "Check for new mail" }));

    await expect.poll(() => api.syncs).toEqual([2]);
  });
});
