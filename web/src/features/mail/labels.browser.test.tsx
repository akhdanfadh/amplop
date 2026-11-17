import { RouterProvider } from "@tanstack/react-router";
import { afterEach, describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import type { ConversationRow, PlaceCount } from "@/api/generated/model";
import { appRouter } from "@/test/app-router";
import { conversation, listedAccount, mailApi, type MailApi } from "@/test/mail-api";

// Labels' ids, as List Places gives them.
const clients = "00000000-0000-4000-8000-0000000000c1";
const acme = "00000000-0000-4000-8000-0000000000c2";
const hiring = "00000000-0000-4000-8000-0000000000c3";
const family = "00000000-0000-4000-8000-0000000000c4";
const reviews = "00000000-0000-4000-8000-0000000000c5";
const vendors = "00000000-0000-4000-8000-0000000000c6";

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

// open renders the app at address over rows, of two accounts, Work's and
// Home's, with their labels, Work's places when given, and returns its
// router once the reading pane shows.
async function open(rows: ConversationRow[], address: string, places = workLabels) {
  api = mailApi(rows, [listedAccount(1, "ana@work.example"), listedAccount(2, "ana@home.example")]);
  api.places = [...places, label(2, family, "Family")];
  const router = await appRouter(address, api.server);
  await render(<RouterProvider router={router} />);
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

describe("Move to after the session ended", () => {
  it("rolls back and says why while the compose window keeps its message", async () => {
    const row = conversation(1, { accountId: 1 });
    await open([row], `/mail/inbox/${row.id}`);
    await userEvent.click(page.getByRole("button", { name: "Compose" }).first());
    await userEvent.type(page.getByRole("textbox", { name: "Subject" }), "Lunch");
    api.signedOut = true;

    await more("Move to");
    await userEvent.click(page.getByRole("menuitem", { name: "Clients/Acme" }));

    // The session-end rule keeps the page for the message; the move, like
    // any failed action, rolls back and says why (web/README.md "Routes").
    await expect.element(page.getByRole("region", { name: "Signed out" })).toBeVisible();
    await expect.element(page.getByText(/Couldn't move/)).toBeVisible();
    await expect.element(page.getByRole("textbox", { name: "Subject" })).toHaveValue("Lunch");
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
    // No label matches; the account's labels can change, so it offers one.
    await expect.element(page.getByRole("menuitem", { name: 'New label "zzq"…' })).toBeVisible();
    await userEvent.click(page.getByRole("button", { name: "Clear filter" }));

    await expect.element(filter).toHaveValue("");
    await expect.element(page.getByRole("menuitemcheckbox", { name: "Hiring" })).toBeVisible();
  });
});

describe("Labels at the contract's limit", () => {
  it("applies 100 labels added, and waits with words at 101", async () => {
    const row = conversation(1, { accountId: 1 });
    api = mailApi([row], [listedAccount(1, "ana@work.example")]);
    // 101 labels, Label 001 to Label 101, as List Places gives them.
    api.places = Array.from({ length: 101 }, (_, i) => {
      const n = String(i + 1).padStart(3, "0");
      return label(1, `00000000-0000-4000-8000-000000001${n}`, `Label ${n}`);
    });
    const router = await appRouter(`/mail/inbox/${row.id}`, api.server);
    await render(<RouterProvider router={router} />);
    await expect.element(toolbar()).toBeVisible();

    await more("Labels");
    const items = page.getByRole("menuitemcheckbox");
    await expect.element(items.nth(100)).toBeInTheDocument();
    await expect.element(page.getByRole("searchbox", { name: "Filter labels" })).toHaveFocus();
    // One press after another, as a member ticks them, from the keyboard:
    // down to the next label and Enter, 101 times, sent as one piece of
    // typing. A pointer press on each took the test 11 s.
    await userEvent.keyboard("{ArrowDown}{Enter}".repeat(101));
    await expect.element(items.nth(100)).toHaveAttribute("aria-checked", "true");
    expect(items.elements().filter((item) => item.ariaChecked === "true")).toHaveLength(101);
    const apply = page.getByRole("button", { name: "Apply" });
    await expect.element(page.getByText("Add or remove up to 100 at once.")).toBeVisible();
    await expect.element(apply).toBeDisabled();
    await userEvent.click(items.nth(100));

    await expect.element(apply).toBeEnabled();
    expect(page.getByText("Add or remove up to 100 at once.").elements()).toHaveLength(0);
  });
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

describe("label chips", () => {
  it("marks the Inbox and the conversation's labels, and takes a label off at once", async () => {
    const row = conversation(1, { accountId: 1, labelIds: [acme] });
    await open([row], `/mail/inbox/${row.id}`);
    const chips = page.getByRole("grid", { name: "Labels" });
    await expect.element(chips.getByRole("row", { name: "Inbox" })).toBeVisible();
    const chip = chips.getByRole("row", { name: "Clients/Acme" });
    await expect.element(chip).toBeVisible();

    await userEvent.click(chip.getByRole("button"));

    await expect.poll(() => api.changes).toEqual([[{ ids: [row.id], remove: [acme] }]]);
    await expect
      .element(page.getByText("Removed from Clients/Acme", { exact: true }))
      .toBeVisible();
    await expect.element(chip).not.toBeInTheDocument();
  });

  it("archives from the Inbox's chip, as Archive does", async () => {
    const row = conversation(1, { accountId: 1 });
    await open([row], `/mail/all/${row.id}`);

    await userEvent.click(
      page
        .getByRole("grid", { name: "Labels" })
        .getByRole("row", { name: "Inbox" })
        .getByRole("button"),
    );

    await expect.poll(() => api.actions).toEqual([{ action: "archive", ids: [row.id] }]);
  });
});

describe("picked conversations of several accounts", () => {
  it("offers each account's labels under its name, says when one has none, and moves one account's", async () => {
    const work = conversation(3, { accountId: 1, subject: "Contract renewal" });
    const home = conversation(2, { accountId: 2, subject: "Internet bill" });
    const spare = conversation(1, { accountId: 3, subject: "Welcome aboard" });
    api = mailApi(
      [work, home, spare],
      [
        listedAccount(1, "ana@work.example"),
        listedAccount(2, "ana@home.example"),
        listedAccount(3, "ana@spare.example"),
      ],
    );
    api.places = [...workLabels, label(2, family, "Family")];
    const router = await appRouter("/mail/inbox", api.server);
    await render(<RouterProvider router={router} />);
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
      .element(page.getByText("No labels in ana@spare.example yet. Type a name to make one."))
      .toBeVisible();
    await userEvent.click(page.getByRole("menuitem", { name: "Clients/Acme" }));

    await expect
      .poll(() => api.changes)
      .toEqual([[{ ids: [work.id], add: [acme], remove: ["inbox"] }]]);
  });
});

describe("a label change's failure", () => {
  it("stays said when another label's change on the conversation works", async () => {
    const row = conversation(1, { accountId: 1, labelIds: [acme, hiring] });
    await open([row], `/mail/all/${row.id}`);
    const chips = page.getByRole("grid", { name: "Labels" });
    api.refuse[row.id] = "failed";
    await userEvent.click(chips.getByRole("row", { name: "Clients/Acme" }).getByRole("button"));
    const error = page.getByText("Couldn't change labels on the conversation");
    await expect.element(error).toBeVisible();
    await expect.element(chips.getByRole("row", { name: "Clients/Acme" })).toBeVisible();

    delete api.refuse[row.id];
    await userEvent.click(chips.getByRole("row", { name: "Hiring" }).getByRole("button"));

    await expect.element(page.getByText("Removed from Hiring", { exact: true })).toBeVisible();
    await expect.element(error).toBeVisible();
  });

  it("closes once that label changed again, though the change named another account's too", async () => {
    const work = conversation(2, { accountId: 1, subject: "Contract renewal", labelIds: [acme] });
    const home = conversation(1, { accountId: 2, subject: "Internet bill", labelIds: [family] });
    await open([work, home], `/mail/all/${work.id}`);
    await pick("Contract renewal");
    await pick("Internet bill");
    api.refuse[work.id] = "failed";
    await userEvent.click(
      page.getByRole("toolbar", { name: "Picked conversations" }).getByRole("button", {
        name: "More actions",
      }),
    );
    await userEvent.click(page.getByRole("menuitem", { name: "Labels…" }));
    await userEvent.click(page.getByRole("menuitemcheckbox", { name: "Clients/Acme" }));
    await userEvent.click(page.getByRole("menuitemcheckbox", { name: "Family" }));
    await userEvent.click(page.getByRole("button", { name: "Apply" }));
    const error = page.getByText("Couldn't change labels on 1 conversation");
    await expect.element(error).toBeVisible();

    delete api.refuse[work.id];
    const chip = page.getByRole("grid", { name: "Labels" }).getByRole("row", {
      name: "Clients/Acme",
    });
    await userEvent.click(chip.getByRole("button"));

    await expect
      .element(page.getByText("Removed from Clients/Acme", { exact: true }))
      .toBeVisible();
    await expect.element(error).not.toBeInTheDocument();
  });
});

// shownMarks returns the marks the list's first row shows, by their text,
// and whether each ends inside the row's line and is no narrower than a
// chip's floor (3.5rem).
function shownMarks() {
  const row = page.getByRole("grid", { name: "Conversations" }).getByRole("row").first().element();
  const marks = row.querySelector("[aria-hidden=true][title]");
  if (!marks?.parentElement) return { names: [], fit: true };
  const end = marks.parentElement.getBoundingClientRect().right;
  const floor = 3.5 * Number.parseFloat(getComputedStyle(document.documentElement).fontSize);
  const shown = [...marks.children].filter((mark) => mark.checkVisibility());
  return {
    names: shown.map((mark) => mark.textContent),
    fit: shown.every((mark) => {
      const box = mark.getBoundingClientRect();
      const chip = !mark.textContent.startsWith("+");
      return box.right <= end + 0.5 && (!chip || box.width >= floor - 0.5);
    }),
  };
}

describe("row marks", () => {
  afterEach(() => {
    document.documentElement.style.fontSize = "";
  });

  it("end the preview with the Inbox and the first label, then how many more, all named for screen readers", async () => {
    const row = conversation(1, { accountId: 1, labelIds: [hiring, acme, clients] });
    await open([row], `/mail/all/${row.id}`);

    // The sidebar's order, two marks in all, the Inbox one of them.
    await expect.poll(() => shownMarks().names).toEqual(["Inbox", "Clients", "+2"]);
    await expect
      .element(page.getByRole("grid", { name: "Conversations" }).getByRole("row").first())
      .toHaveAccessibleName(
        expect.stringMatching(/In Inbox, .*Labels Clients, Clients\/Acme, and Hiring$/),
      );
  });

  it("show two, one, then none as the row narrows, each cut to fit, never past the line", async () => {
    const row = conversation(1, {
      accountId: 1,
      inbox: false,
      labelIds: [reviews, vendors, clients, acme, hiring],
    });
    await open([row], `/mail/all/${row.id}`, [
      ...workLabels,
      label(1, reviews, "Annual business reviews and planning"),
      label(1, vendors, "Board meetings and vendor contracts"),
    ]);

    await expect
      .poll(() => shownMarks())
      .toEqual({
        names: [
          "Annual business reviews and planning",
          "Board meetings and vendor contracts",
          "+3",
        ],
        fit: true,
      });
    await page.viewport(1024, 900);
    await expect
      .poll(() => shownMarks())
      .toEqual({ names: ["Annual business reviews and planning", "+4"], fit: true });
    // Text at 200%, as a member may set it: the line is too narrow for one.
    document.documentElement.style.fontSize = "200%";
    await expect.poll(() => shownMarks()).toEqual({ names: [], fit: true });
  });

  it("leave out the label whose list they are in", async () => {
    const row = conversation(1, { accountId: 1, labelIds: [clients, hiring] });
    await open([row], `/mail/${clients}/${row.id}?account=1`);

    await expect.poll(() => shownMarks().names).toEqual(["Inbox", "Hiring"]);
  });
});
