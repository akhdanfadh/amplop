import { RouterProvider } from "@tanstack/react-router";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { cleanup, render } from "vitest-browser-react";
import type { ConversationRow } from "@/api/generated/model";
import { closeToasts } from "@/components/ui/toast";
import { clearAccountCache } from "@/features/auth/session-cache";
import { appRouter, memberSession } from "@/test/app-router";
import { conversation, listedAccount, mailApi, type MailApi } from "@/test/mail-api";

let api: MailApi;
let current: Awaited<ReturnType<typeof appRouter>> | undefined;

afterEach(async () => {
  closeToasts();
  await cleanup();
  if (current) clearAccountCache(current.options.context.queryClient);
  current = undefined;
  vi.unstubAllGlobals();
});

// open renders the app wide, or width wide, at /mail/<place>/<id of
// rows[open]>, over rows, one account's, and returns its router. A list of
// slowPlace answers its pages 400 ms late, as a real server can.
async function open(
  rows: ConversationRow[],
  index: number,
  place = "inbox",
  slowPlace?: string,
  width = 1440,
) {
  await page.viewport(width, 900);
  api = mailApi(rows, [listedAccount(1, "ana@example.com")]);
  const router = await appRouter(`/mail/${place}/${rows[index].id}`, memberSession, (url, init) => {
    // The session is the member's until api signs out.
    if (new URL(url, location.href).pathname === "/api/v0/sessions/current") {
      return api.signedOut ? api.fetch(url, init) : undefined;
    }
    if (slowPlace && url.includes(`place=${slowPlace}`)) {
      return new Promise((done) => setTimeout(done, 400)).then(() => api.fetch(url, init));
    }
    return api.fetch(url, init);
  });
  await render(<RouterProvider router={router} />);
  current = router;
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

// hold makes api's answers to requests to path wait, each until released
// in turn, and returns what releases the next and how many wait.
function hold(path: string): { release: () => void; waiting: () => number } {
  const fetch = api.fetch;
  const waiting: (() => void)[] = [];
  api.fetch = async (input, init) => {
    const url = input instanceof Request ? input.url : String(input);
    if (new URL(url, location.href).pathname !== path) return fetch(input, init);
    await new Promise<void>((done) => waiting.push(done));
    return fetch(input, init);
  };
  return { release: () => waiting.shift()?.(), waiting: () => waiting.length };
}

describe("the reading pane's actions", () => {
  it("archives from the Inbox: back to the list, the next row focused", async () => {
    const rows = [conversation(3), conversation(2), conversation(1)];
    const router = await open(rows, 1);

    await userEvent.click(toolbar().getByRole("button", { name: "Archive" }));

    await expect.poll(() => router.state.location.pathname).toBe("/mail/inbox");
    await expect.element(list().getByRole("row", { name: /Subject 2/ })).not.toBeInTheDocument();
    await expect.element(list().getByRole("row", { name: /Subject 1/ })).toHaveFocus();
    await expect.poll(() => api.actions).toEqual([{ action: "archive", ids: [rows[1].id] }]);
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
    const router = await open(rows, 0, "inbox", "trash");

    await userEvent.click(toolbar().getByRole("button", { name: "Delete" }));
    await expect.poll(() => router.state.location.pathname).toBe("/mail/inbox");
    await userEvent.click(
      page.getByRole("navigation", { name: "Mail" }).getByRole("link", { name: /Trash/ }).first(),
    );

    await expect.element(list().getByRole("row", { name: /Subject 3/ })).toBeVisible();
    await expect.element(list().getByRole("row", { name: /Subject 1/ })).toBeVisible();
  });

  it("stars in place, the star pressed", async () => {
    const rows = [conversation(1)];
    const router = await open(rows, 0);

    await userEvent.click(toolbar().getByRole("button", { name: "Star" }));

    await expect
      .element(toolbar().getByRole("button", { name: "Star" }))
      .toHaveAttribute("aria-pressed", "true");
    expect(router.state.location.pathname).toBe(`/mail/inbox/${rows[0].id}`);
  });

  it("goes back to the list after Mark unread, whatever the list", async () => {
    const rows = [conversation(1)];
    const router = await open(rows, 0);

    await userEvent.click(toolbar().getByRole("button", { name: "Mark unread" }));

    await expect.poll(() => router.state.location.pathname).toBe("/mail/inbox");
    await expect.poll(() => api.conversations[0].unread).toBe(true);
  });

  it("offers a Trash list's actions, and More with Report spam only elsewhere", async () => {
    await open([conversation(1, { trash: true, inbox: false, allMail: false })], 0, "trash");

    await expect.element(toolbar().getByRole("button", { name: "Move to Inbox" })).toBeVisible();
    expect(toolbar().getByRole("button", { name: "Delete" }).elements()).toHaveLength(0);
    // No provider link from the fake API, and nothing else for More.
    expect(toolbar().getByRole("button", { name: "More actions" }).elements()).toHaveLength(0);
  });

  it("keeps Report spam in More", async () => {
    await open([conversation(1)], 0);

    await userEvent.click(toolbar().getByRole("button", { name: "More actions" }));
    await userEvent.click(page.getByRole("menuitem", { name: "Report spam" }));

    await expect
      .poll(() => api.actions)
      .toEqual([{ action: "spam", ids: [api.conversations[0].id] }]);
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
    await page.viewport(1440, 900);
    const router = await appRouter("/mail/starred", memberSession, (url, init) => {
      if (new URL(url, location.href).pathname === "/api/v0/sessions/current") return undefined;
      return api.fetch(url, init);
    });
    await render(<RouterProvider router={router} />);
    current = router;
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
    await page.viewport(1440, 900);
    const router = await appRouter("/mail/inbox", memberSession, (url, init) => {
      if (new URL(url, location.href).pathname === "/api/v0/sessions/current") return undefined;
      return api.fetch(url, init);
    });
    await render(<RouterProvider router={router} />);
    current = router;
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
    await page.viewport(1440, 900);
    const router = await appRouter("/mail/starred", memberSession, (url, init) => {
      if (new URL(url, location.href).pathname === "/api/v0/sessions/current") return undefined;
      return api.fetch(url, init);
    });
    await render(<RouterProvider router={router} />);
    current = router;
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
    toolbar().getByRole("button", { name: "Archive" }).element().focus();

    await userEvent.keyboard("{Enter}");

    await expect.element(toolbar().getByRole("button", { name: "Move to Inbox" })).toHaveFocus();
    // Move to Inbox, not Archive's box.
    expect(document.activeElement?.querySelector(".lucide-archive-restore")).not.toBeNull();
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

  it("hides the pinned toolbar's subject where it would be a few letters, e.g. a phone", async () => {
    await open([conversation(1)], 0, "inbox", undefined, 390);
    expect(getComputedStyle(subjectCopy()).display).toBe("none");

    await page.viewport(1440, 900);
    await expect.poll(() => getComputedStyle(subjectCopy()).display).not.toBe("none");
  });
});
