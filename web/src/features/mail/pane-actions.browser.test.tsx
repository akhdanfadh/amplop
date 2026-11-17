import { RouterProvider } from "@tanstack/react-router";
import { describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import type { ConversationRow } from "@/api/generated/model";
import { clearAccountCache } from "@/features/auth/session-cache";
import { shortcutModifier } from "@/lib/shortcut";
import { appRouter } from "@/test/app-router";
import { fakeClock } from "@/test/clock";
import { conversation, listedAccount, mailApi, message, type MailApi } from "@/test/mail-api";

let api: MailApi;

// How late a slow place's list answers.
const lateMs = 400;

// open renders the app wide, or width wide, at /mail/<place>/<id of
// rows[open]>, over rows, one account's, and returns its router. A list of
// slowPlace answers its pages lateMs late, as a real server can.
async function open(
  rows: ConversationRow[],
  index: number,
  place = "inbox",
  slowPlace?: string,
  width = 1440,
) {
  await page.viewport(width, 900);
  api = mailApi(rows, [listedAccount(1, "ana@example.com")]);
  api.server.on("GET", "/api/v0/conversations", async ({ url }, earlier) => {
    if (slowPlace && url.searchParams.get("place") === slowPlace) {
      await new Promise((done) => setTimeout(done, lateMs));
    }
    return earlier();
  });
  const router = await appRouter(`/mail/${place}/${rows[index].id}`, api.server);
  await render(<RouterProvider router={router} />);
  await expect.element(toolbar()).toBeVisible();
  return router;
}

// toolbar returns the reading pane's toolbar.
function toolbar() {
  return page.getByRole("toolbar", { name: "Conversation actions" });
}

// barPosition returns the toolbar's bar's position: sticky while pinned,
// else static.
function barPosition(): string {
  return getComputedStyle(toolbar().element().parentElement!).position;
}

// list returns the conversation list.
function list() {
  return page.getByRole("grid", { name: "Conversations" });
}

// subjectCopy returns the faded copy of the subject beside the toolbar.
function subjectCopy(): Element {
  const copy = toolbar().element().parentElement?.querySelector("p[aria-hidden=true]");
  if (!copy) throw new Error("No subject beside the toolbar.");
  return copy;
}

// bar returns the Undo bar, a landmark named Undo.
function bar() {
  return page.getByRole("region", { name: "Undo" });
}

// hold makes api's answers to the writes to path wait, each until
// released in turn, and returns what releases the next and how many wait.
function hold(path: "/api/v0/conversations/actions" | "/api/v0/conversations/changes"): {
  release: () => void;
  waiting: () => number;
} {
  const waiting: (() => void)[] = [];
  api.server.on("POST", path, async (_, earlier) => {
    await new Promise<void>((done) => waiting.push(done));
    return earlier();
  });
  return { release: () => waiting.shift()?.(), waiting: () => waiting.length };
}

// withMessage makes api answer the read of row with one message, so the
// pane offers Reply.
function withMessage(row: ConversationRow): void {
  api.messages[row.id] = [message(row)];
}

// press sends a key press to target, as from the keyboard, and returns
// whether a handler took it.
function press(target: Element, init: KeyboardEventInit): boolean {
  const event = new KeyboardEvent("keydown", { bubbles: true, cancelable: true, ...init });
  target.dispatchEvent(event);
  return event.defaultPrevented;
}

// The key press that runs Undo, Ctrl+Z, which isShortcut takes on any
// platform, as it takes Cmd+Z.
const ctrlZ = { key: "z", code: "KeyZ", ctrlKey: true };

describe("the reading pane's actions", () => {
  it("archives from the Inbox: back to the list, the next row focused, Undo in the bar puts it back", async () => {
    const rows = [conversation(3), conversation(2), conversation(1)];
    const router = await open(rows, 1);

    await userEvent.click(toolbar().getByRole("button", { name: "Archive" }));

    await expect.poll(() => router.state.location.pathname).toBe("/mail/inbox");
    await expect.element(list().getByRole("row", { name: /Subject 2/ })).not.toBeInTheDocument();
    await expect.element(list().getByRole("row", { name: /Subject 1/ })).toHaveFocus();
    await expect.element(page.getByText("Archived", { exact: true })).toBeVisible();
    await expect
      .poll(() =>
        page
          .getByRole("status")
          .elements()
          .map((e) => e.textContent),
      )
      .toContain("Archived: Subject 2. Find it in All mail.");
    await expect.poll(() => api.actions).toEqual([{ action: "archive", ids: [rows[1].id] }]);

    await userEvent.click(page.getByRole("button", { name: "Undo" }));

    await expect.poll(() => api.actions.at(-1)).toEqual({ action: "changes", ids: [rows[1].id] });
    await expect.element(list().getByRole("row", { name: /Subject 2/ })).toBeVisible();
    // Undo puts it back; it opens nothing.
    expect(router.state.location.pathname).toBe("/mail/inbox");
    await expect.element(page.getByRole("button", { name: "Undo" })).not.toBeInTheDocument();
  });

  it("keeps the Undo while another conversation opens, and Tab reaches it from the list", async () => {
    const rows = [conversation(3), conversation(2), conversation(1)];
    const router = await open(rows, 1);
    await userEvent.click(toolbar().getByRole("button", { name: "Archive" }));
    await expect.element(list().getByRole("row", { name: /Subject 1/ })).toHaveFocus();

    await userEvent.keyboard("{Tab}");
    await expect.element(page.getByRole("button", { name: "Undo" })).toHaveFocus();

    await userEvent.click(list().getByRole("row", { name: /Subject 3/ }));
    await expect.poll(() => router.state.location.pathname).toBe(`/mail/inbox/${rows[0].id}`);
    await expect.element(page.getByRole("button", { name: "Undo" })).toBeVisible();
  });

  it("replaces the Undo with the next action's", async () => {
    const rows = [conversation(3), conversation(2), conversation(1)];
    await open(rows, 1);
    await userEvent.click(toolbar().getByRole("button", { name: "Archive" }));
    await userEvent.click(list().getByRole("row", { name: /Subject 3/ }));
    await userEvent.click(toolbar().getByRole("button", { name: "Delete" }));

    await expect.element(page.getByText("Moved to Trash", { exact: true })).toBeVisible();
    expect(page.getByText("Archived", { exact: true }).elements()).toHaveLength(0);
    expect(page.getByRole("button", { name: "Undo" }).elements()).toHaveLength(1);
  });

  it("deletes an archived conversation from its label, and Undo puts it back archived", async () => {
    const work = "11111111-1111-4111-8111-111111111111";
    const rows = [conversation(1, { inbox: false, labelIds: [work] })];
    await open(rows, 0, "all");

    await userEvent.click(toolbar().getByRole("button", { name: "Delete" }));
    await userEvent.click(page.getByRole("button", { name: "Undo" }));

    await expect.poll(() => api.actions.at(-1)?.action).toBe("changes");
    expect(api.conversations[0]).toMatchObject({ trash: false, inbox: false, labelIds: [work] });
  });

  it("lists a deleted conversation in Trash, though Trash's first page answers late", async () => {
    // The Inbox list, left for Trash, lets its rows go while Trash's first
    // page loads; TanStack DB, patched, then reads that page again rather
    // than the whole place, which inbok refuses (ADR031, TanStack DB gate,
    // rule 9).
    const rows = [
      conversation(3),
      conversation(2),
      conversation(1, { trash: true, inbox: false, allMail: false }),
    ];
    const clock = fakeClock();
    const router = await open(rows, 0, "inbox", "trash");

    await userEvent.click(toolbar().getByRole("button", { name: "Delete" }));
    await expect.poll(() => router.state.location.pathname).toBe("/mail/inbox");
    await userEvent.click(
      page.getByRole("navigation", { name: "Mail" }).getByRole("link", { name: /Trash/ }).first(),
    );
    // Trash's first page is asked for, and answers late.
    await expect.poll(() => api.requests.some((r) => r.includes("place=trash"))).toBe(true);
    await clock.advance(lateMs);

    await expect.element(list().getByRole("row", { name: /Subject 3/ })).toBeVisible();
    await expect.element(list().getByRole("row", { name: /Subject 1/ })).toBeVisible();
  });

  it("archives in All mail in place, its Inbox chip gone, and Move to Inbox brings it back", async () => {
    const rows = [conversation(1)];
    const router = await open(rows, 0, "all");
    await expect.element(list().getByRole("row", { name: /Subject 1, .*In Inbox/ })).toBeVisible();

    await userEvent.click(toolbar().getByRole("button", { name: "Archive" }));

    const row = list().getByRole("row", { name: /Subject 1,/ });
    await expect.poll(() => row.element().getAttribute("aria-label")).not.toContain("In Inbox");
    expect(router.state.location.pathname).toBe(`/mail/all/${rows[0].id}`);

    await userEvent.click(toolbar().getByRole("button", { name: "Move to Inbox" }));
    await expect.element(list().getByRole("row", { name: /Subject 1, .*In Inbox/ })).toBeVisible();
  });

  it("closes an Undo's failure once Move to Inbox puts the conversation back", async () => {
    const rows = [conversation(1)];
    await open(rows, 0, "all");
    await userEvent.click(toolbar().getByRole("button", { name: "Archive" }));
    await expect.poll(() => api.actions).toHaveLength(1);
    api.refuse[rows[0].id] = "failed";
    await userEvent.click(bar().getByRole("button", { name: "Undo" }));
    const error = page.getByText("Couldn't undo");
    await expect.element(error).toBeVisible();

    delete api.refuse[rows[0].id];
    await userEvent.click(toolbar().getByRole("button", { name: "Move to Inbox" }));

    await expect.element(error).not.toBeInTheDocument();
  });

  it("stars in place, with no Undo, the star pressed", async () => {
    const rows = [conversation(1)];
    const router = await open(rows, 0);

    await userEvent.click(toolbar().getByRole("button", { name: "Star" }));

    await expect
      .element(toolbar().getByRole("button", { name: "Star" }))
      .toHaveAttribute("aria-pressed", "true");
    expect(router.state.location.pathname).toBe(`/mail/inbox/${rows[0].id}`);
    expect(page.getByRole("button", { name: "Undo" }).elements()).toHaveLength(0);
  });

  it("goes back to the list after Mark unread, whatever the list", async () => {
    const rows = [conversation(1)];
    const router = await open(rows, 0);

    await userEvent.click(toolbar().getByRole("button", { name: "Mark unread" }));

    await expect.poll(() => router.state.location.pathname).toBe("/mail/inbox");
    await expect.poll(() => api.conversations[0].unread).toBe(true);
  });

  it("offers a Trash list's actions, and More with Move to but no Labels or Report spam", async () => {
    await open([conversation(1, { trash: true, inbox: false, allMail: false })], 0, "trash");

    await expect.element(toolbar().getByRole("button", { name: "Move to Inbox" })).toBeVisible();
    expect(toolbar().getByRole("button", { name: "Delete" }).elements()).toHaveLength(0);
    // More holds Move to…, Trash being a place to move out of, but not
    // Labels…, as a row only in Trash lists no labels; no Report spam,
    // and no provider link from the fake API.
    await userEvent.click(toolbar().getByRole("button", { name: "More actions" }));
    await expect.element(page.getByRole("menuitem", { name: "Move to…" })).toBeVisible();
    expect(page.getByRole("menuitem", { name: "Labels…" }).elements()).toHaveLength(0);
    expect(page.getByRole("menuitem", { name: "Report spam" }).elements()).toHaveLength(0);
  });

  it("keeps Report spam in More", async () => {
    await open([conversation(1)], 0);

    await userEvent.click(toolbar().getByRole("button", { name: "More actions" }));
    await userEvent.click(page.getByRole("menuitem", { name: "Report spam" }));

    await expect
      .poll(() => api.actions)
      .toEqual([{ action: "spam", ids: [api.conversations[0].id] }]);
    await expect.element(page.getByText("Reported as spam", { exact: true })).toBeVisible();
  });

  it("pins the toolbar while its buttons fit one row, not at large text on a phone", async () => {
    await open([conversation(1)], 0);
    await page.viewport(390, 800);
    await expect.poll(barPosition).toBe("sticky");

    document.documentElement.style.fontSize = "200%";
    try {
      await expect.poll(barPosition).toBe("static");
    } finally {
      document.documentElement.style.fontSize = "";
    }
  });

  it("sets a subject's direction by its first letter", async () => {
    await open([conversation(1, { subject: "مرحبا بالفريق!" })], 0);

    await expect.element(page.getByRole("heading", { level: 2 })).toHaveAttribute("dir", "auto");
  });

  it("leaves the member on the list with why when the action is refused", async () => {
    const rows = [conversation(2), conversation(1)];
    const router = await open(rows, 0);
    api.refuse[rows[0].id] = "failed";

    await userEvent.click(toolbar().getByRole("button", { name: "Archive" }));

    await expect.poll(() => router.state.location.pathname).toBe("/mail/inbox");
    await expect.element(page.getByText("Couldn't archive the conversation")).toBeVisible();
    await expect.element(list().getByRole("row", { name: /Subject 2/ })).toBeVisible();
    await expect.element(page.getByRole("button", { name: "Undo" })).not.toBeInTheDocument();
  });

  it("removes a conversation the provider no longer has, back on the list", async () => {
    const rows = [conversation(2), conversation(1)];
    const router = await open(rows, 0);
    // Gone at Gmail, and from inbok's lists once its poll noticed.
    api.refuse[rows[0].id] = "not-found";
    api.conversations = [rows[1]];

    await userEvent.click(toolbar().getByRole("button", { name: "Star" }));

    await expect.poll(() => router.state.location.pathname).toBe("/mail/inbox");
    await expect.element(page.getByText("It's no longer in Gmail.")).toBeVisible();
    await expect.element(list().getByRole("row", { name: /Subject 2/ })).not.toBeInTheDocument();
  });

  it("goes to its list, not back, when no row of it opened the conversation", async () => {
    // e.g. Settings, then Back to mail, a link to the conversation.
    const rows = [conversation(2), conversation(1)];
    api = mailApi(rows, [listedAccount(1, "ana@example.com")]);
    const router = await appRouter("/mail/starred", api.server);
    await render(<RouterProvider router={router} />);
    await router.navigate({
      to: "/mail/$place/$conversationId",
      params: { place: "inbox", conversationId: rows[0].id },
    });
    await expect.element(toolbar()).toBeVisible();

    await userEvent.click(toolbar().getByRole("button", { name: "Archive" }));

    await expect.poll(() => router.state.location.pathname).toBe("/mail/inbox");
  });

  it("goes back through history to the list whose row opened the conversation", async () => {
    const rows = [conversation(2), conversation(1)];
    api = mailApi(rows, [listedAccount(1, "ana@example.com")]);
    const router = await appRouter("/mail/inbox", api.server);
    await render(<RouterProvider router={router} />);
    await userEvent.click(list().getByRole("row", { name: /Subject 2/ }));
    await expect.element(toolbar()).toBeVisible();

    await userEvent.click(toolbar().getByRole("button", { name: "Archive" }));

    await expect.poll(() => router.state.location.pathname).toBe("/mail/inbox");
    // Back where the list was, with no entry added.
    expect(router.history.canGoBack()).toBe(false);
  });

  it("goes to its list, not back, after a row replaced a conversation opened by its address", async () => {
    // Another page, then the first conversation by its address.
    const rows = [conversation(2), conversation(1)];
    api = mailApi(rows, [listedAccount(1, "ana@example.com")]);
    const router = await appRouter("/mail/starred", api.server);
    await render(<RouterProvider router={router} />);
    await router.navigate({
      to: "/mail/$place/$conversationId",
      params: { place: "inbox", conversationId: rows[0].id },
    });
    await expect.element(toolbar()).toBeVisible();
    await userEvent.click(list().getByRole("row", { name: /Subject 1/ }));
    await expect.poll(() => router.state.location.pathname).toBe(`/mail/inbox/${rows[1].id}`);

    await userEvent.click(toolbar().getByRole("button", { name: "Archive" }));

    await expect.poll(() => router.state.location.pathname).toBe("/mail/inbox");
    await expect.element(list().getByRole("row", { name: /Subject 2/ })).toBeVisible();
  });

  it("keeps focus on the button whose action changes, e.g. Archive to Move to Inbox", async () => {
    await open([conversation(1)], 0, "all");
    // A button's icon has no role or name of its own: its drawing is read.
    const drawing = (name: string) =>
      toolbar().getByRole("button", { name }).element().querySelector("svg")?.innerHTML;
    const archive = drawing("Archive");
    expect(archive).toBeTruthy();
    toolbar().getByRole("button", { name: "Archive" }).element().focus();

    await userEvent.keyboard("{Enter}");

    await expect.element(toolbar().getByRole("button", { name: "Move to Inbox" })).toHaveFocus();
    // Its icon changed with its name: no longer Archive's box.
    expect(drawing("Move to Inbox")).toBeTruthy();
    expect(drawing("Move to Inbox")).not.toBe(archive);
  });

  it("runs a double click on Archive once, not Archive then Move to Inbox", async () => {
    const rows = [conversation(2), conversation(1)];
    const router = await open(rows, 0);

    await userEvent.dblClick(toolbar().getByRole("button", { name: "Archive" }));

    await expect.poll(() => router.state.location.pathname).toBe("/mail/inbox");
    await expect.poll(() => api.actions).toEqual([{ action: "archive", ids: [rows[0].id] }]);
  });

  it("focuses the next row on a phone too, not the place's heading", async () => {
    const rows = [conversation(3), conversation(2), conversation(1)];
    const router = await open(rows, 1, "inbox", undefined, 390);

    await userEvent.click(toolbar().getByRole("button", { name: "Archive" }));

    await expect.poll(() => router.state.location.pathname).toBe("/mail/inbox");
    await expect.element(list().getByRole("row", { name: /Subject 1/ })).toHaveFocus();
  });

  it("focuses the conversation marked unread, still in its list", async () => {
    const rows = [conversation(3), conversation(2), conversation(1)];
    await open(rows, 1);

    await userEvent.click(toolbar().getByRole("button", { name: "Mark unread" }));

    await expect.element(list().getByRole("row", { name: /Subject 2/ })).toHaveFocus();
  });

  it("focuses the place's heading when no row is left in the list", async () => {
    await open([conversation(1)], 0);

    await userEvent.click(toolbar().getByRole("button", { name: "Archive" }));

    await expect.element(page.getByRole("heading", { level: 1, name: "Inbox" })).toHaveFocus();
  });

  it("hands focus back to the row it came from after Undo, not to a star", async () => {
    const rows = [conversation(3), conversation(2), conversation(1)];
    await open(rows, 1);
    await userEvent.click(toolbar().getByRole("button", { name: "Archive" }));
    await expect.element(list().getByRole("row", { name: /Subject 1/ })).toHaveFocus();

    await userEvent.keyboard("{Tab}");
    await expect.element(bar().getByRole("button", { name: "Undo" })).toHaveFocus();
    await userEvent.keyboard("{Enter}");

    await expect.element(list().getByRole("row", { name: /Subject 1/ })).toHaveFocus();
    // For screen readers, once it has run.
    await expect
      .poll(() =>
        page
          .getByRole("status")
          .elements()
          .map((e) => e.textContent),
      )
      .toContain("Undone: Subject 2.");
  });

  it("hands focus back when a refusal closes the Undo while focus is on it", async () => {
    const rows = [conversation(3), conversation(2), conversation(1)];
    await open(rows, 1);
    const actions = hold("/api/v0/conversations/actions");
    await userEvent.click(toolbar().getByRole("button", { name: "Archive" }));
    await expect.element(list().getByRole("row", { name: /Subject 1/ })).toHaveFocus();
    await userEvent.keyboard("{Tab}");
    await expect.element(bar().getByRole("button", { name: "Undo" })).toHaveFocus();

    api.refuse[rows[1].id] = "failed";
    actions.release();

    await expect.element(bar()).not.toBeInTheDocument();
    await expect.element(list().getByRole("row", { name: /Subject 1/ })).toHaveFocus();
  });

  it("stays on the conversation opened since, when a late answer finds the last one gone", async () => {
    const rows = [conversation(2), conversation(1)];
    const router = await open(rows, 0);
    const actions = hold("/api/v0/conversations/actions");
    api.refuse[rows[0].id] = "not-found";
    await userEvent.click(toolbar().getByRole("button", { name: "Star" }));
    await expect.poll(actions.waiting).toBe(1);
    await userEvent.click(list().getByRole("row", { name: /Subject 1/ }));
    await expect.poll(() => router.state.location.pathname).toBe(`/mail/inbox/${rows[1].id}`);

    actions.release();

    await expect.element(page.getByText("It's no longer in Gmail.")).toBeVisible();
    expect(router.state.location.pathname).toBe(`/mail/inbox/${rows[1].id}`);
  });

  it("says nothing of an Undo that fails after sign-out", async () => {
    const clock = fakeClock();
    const rows = [conversation(2), conversation(1)];
    const router = await open(rows, 0);
    await userEvent.click(toolbar().getByRole("button", { name: "Archive" }));
    const changes = hold("/api/v0/conversations/changes");
    await userEvent.click(bar().getByRole("button", { name: "Undo" }));
    await expect.poll(changes.waiting).toBe(1);

    clearAccountCache(router.options.context.queryClient);
    api.failAction = 500;
    changes.release();

    await expect.poll(() => api.actions.at(-1)?.action).toBe("changes");
    // Two rounds on: the failure, and what it would say, have reached
    // the page.
    await clock.advance(100);
    await clock.advance(100);
    expect(page.getByText("Couldn't undo").query()).toBeNull();
  });

  it("opens sign-in when an Undo finds the session ended", async () => {
    const rows = [conversation(2), conversation(1)];
    const router = await open(rows, 0);
    await userEvent.click(toolbar().getByRole("button", { name: "Archive" }));
    await expect.poll(() => api.actions).toHaveLength(1);
    // The session ends as the Undo is sent: any request before it would
    // open sign-in first.
    api.server.on("POST", "/api/v0/conversations/changes", (_, earlier) => {
      api.signedOut = true;
      return earlier();
    });

    await userEvent.click(bar().getByRole("button", { name: "Undo" }));

    await expect.poll(() => router.state.location.pathname).toBe("/sign-in");
    expect(page.getByText("Couldn't undo").query()).toBeNull();
  });

  it("docks the Undo bar at the bottom of the list beside the reading pane", async () => {
    const rows = [conversation(2), conversation(1)];
    await open(rows, 0, "all");

    // Archive keeps the conversation open in All mail.
    await userEvent.click(toolbar().getByRole("button", { name: "Archive" }));

    await expect.element(bar()).toBeVisible();
    expect(bar().element().closest("main")).toBe(list().element().closest("main"));
    const box = bar().element().getBoundingClientRect();
    const undo = bar().getByRole("button", { name: "Undo" }).element().getBoundingClientRect();
    // The list's width, so Undo sits near the words.
    expect(box.width).toBeLessThan(500);
    expect(undo.right).toBeLessThanOrEqual(box.right);
  });

  it("docks the Undo bar under the open conversation on a phone", async () => {
    const rows = [conversation(2), conversation(1)];
    await open(rows, 0, "all", undefined, 390);

    await userEvent.click(toolbar().getByRole("button", { name: "Archive" }));

    // The list is hidden while the conversation shows; the bar is not.
    await expect.element(bar()).toBeVisible();
    expect(bar().element().closest("main")).toBeNull();
  });

  it("hides the pinned toolbar's subject where it would be a few letters, e.g. a phone", async () => {
    await open([conversation(1)], 0, "inbox", undefined, 390);
    expect(getComputedStyle(subjectCopy()).display).toBe("none");

    await page.viewport(1440, 900);
    await expect.poll(() => getComputedStyle(subjectCopy()).display).not.toBe("none");
  });
});

describe("the Undo bar's keys", () => {
  it("undoes with Ctrl+Z from inside the conversation", async () => {
    const rows = [conversation(1)];
    await open(rows, 0, "all");
    await userEvent.click(toolbar().getByRole("button", { name: "Archive" }));
    await expect.element(bar()).toBeVisible();

    await userEvent.keyboard("{Control>}z{/Control}");

    await expect.poll(() => api.actions.at(-1)?.action).toBe("changes");
    await expect.element(bar()).not.toBeInTheDocument();
    await expect.element(list().getByRole("row", { name: /Subject 1, .*In Inbox/ })).toBeVisible();
  });

  it("leaves Ctrl+Z to the reply editor, Ctrl+Shift+Z, and an open menu, dialog, or sheet", async () => {
    const rows = [conversation(2), conversation(1)];
    api = mailApi(rows, [listedAccount(1, "ana@example.com")]);
    await open(rows, 0, "all");
    withMessage(rows[0]);
    await userEvent.click(toolbar().getByRole("button", { name: "Archive" }));
    await expect.element(bar()).toBeVisible();

    expect(press(toolbar().element(), { ...ctrlZ, shiftKey: true })).toBe(false);

    await userEvent.click(toolbar().getByRole("button", { name: "More actions" }));
    const menu = page.getByRole("menu");
    await expect.element(menu).toBeVisible();
    expect(press(menu.element(), ctrlZ)).toBe(false);
    await userEvent.keyboard("{Escape}");

    const dialog = document.createElement("div");
    dialog.setAttribute("role", "dialog");
    const inside = document.createElement("button");
    dialog.append(inside);
    document.body.append(dialog);
    try {
      expect(press(inside, ctrlZ)).toBe(false);
      // A side sheet, e.g. the sidebar's drawer, is a dialog too.
      dialog.dataset.slot = "sheet";
      expect(press(inside, ctrlZ)).toBe(false);
    } finally {
      dialog.remove();
    }

    await userEvent.click(page.getByRole("button", { name: "Reply", exact: true }).last());
    const editor = page.getByRole("textbox", { name: "Message" });
    await expect.element(editor).toHaveFocus();
    expect(press(editor.element(), ctrlZ)).toBe(false);

    expect(api.actions.map((a) => a.action)).toEqual(["archive"]);
    await expect.element(bar()).toBeVisible();
  });

  it("leaves Ctrl+Z to the browser with no Undo showing", async () => {
    await open([conversation(1)], 0);

    expect(press(toolbar().element(), ctrlZ)).toBe(false);
    expect(api.actions).toEqual([]);
  });

  it("names the shortcut in Undo's tooltip", async () => {
    const rows = [conversation(2), conversation(1)];
    await open(rows, 0);
    await userEvent.click(toolbar().getByRole("button", { name: "Archive" }));
    await expect.element(list().getByRole("row", { name: /Subject 1/ })).toHaveFocus();
    const key = shortcutModifier();

    await userEvent.keyboard("{Tab}");

    await expect.poll(() => page.getByRole("tooltip").query()?.textContent).toBe(`Undo (${key}+Z)`);
    // And which conversation it puts back.
    await expect
      .element(bar().getByRole("button", { name: "Undo" }))
      .toHaveAccessibleDescription(`Archived Subject 2 ${key}+Z`);
  });

  it("is reached with F6", async () => {
    const rows = [conversation(1)];
    await open(rows, 0, "all");
    await userEvent.click(toolbar().getByRole("button", { name: "Archive" }));
    await expect.element(bar()).toBeVisible();

    await userEvent.keyboard("{F6}");

    await expect.poll(() => bar().element().contains(document.activeElement)).toBe(true);
  });
});
