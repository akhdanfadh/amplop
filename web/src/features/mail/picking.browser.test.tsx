import { RouterProvider } from "@tanstack/react-router";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { cleanup, render } from "vitest-browser-react";
import type { ConversationRow } from "@/api/generated/model";
import { clearAccountCache } from "@/features/auth/session-cache";
import { appRouter, memberSession } from "@/test/app-router";
import { conversation, listedAccount, mailApi, type MailApi } from "@/test/mail-api";

let api: MailApi;
let current: Awaited<ReturnType<typeof appRouter>> | undefined;

afterEach(async () => {
  await cleanup();
  if (current) clearAccountCache(current.options.context.queryClient);
  current = undefined;
  vi.unstubAllGlobals();
});

// open renders the app width wide at address over rows, one account's, or
// accounts', and returns its router once the list shows.
async function open(
  rows: ConversationRow[],
  { address = "/mail/inbox", width = 1440, accounts = [listedAccount(1, "ana@example.com")] } = {},
) {
  await page.viewport(width, 900);
  api = mailApi(rows, accounts);
  const router = await appRouter(address, memberSession, (url, init) => {
    const path = new URL(url, location.href).pathname;
    if (path === "/api/v0/sessions/current") {
      return api.signedOut ? api.fetch(url, init) : undefined;
    }
    return api.fetch(url, init);
  });
  await render(<RouterProvider router={router} />);
  current = router;
  await expect.element(list()).toBeVisible();
  return router;
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

// picked expects subject's row to be picked, or not when is is false.
async function picked(subject: string, is = true): Promise<void> {
  await expect.element(row(subject)).toHaveAttribute("aria-selected", String(is));
}

describe("picking", () => {
  it("picks a row by its box, and with Shift every row up to another", async () => {
    await open([conversation(5), conversation(4), conversation(3), conversation(2)]);

    await pickBox("Subject 5");
    await picked("Subject 5");
    await pickBox("Subject 3", { shift: true });

    await expect.element(row("Subject 4")).toHaveAttribute("aria-selected", "true");
    await expect.element(row("Subject 3")).toHaveAttribute("aria-selected", "true");
    await expect.element(row("Subject 2")).toHaveAttribute("aria-selected", "false");
  });

  it("opens a row pressed on its text while rows are picked, and keeps the picks", async () => {
    const rows = [conversation(5), conversation(4), conversation(3)];
    const router = await open(rows);
    await pickBox("Subject 5");

    await userEvent.click(row("Subject 3").getByText("Preview 3"));

    await expect.poll(() => router.state.location.pathname).toBe(`/mail/inbox/${rows[2].id}`);
    await picked("Subject 5");
    await picked("Subject 3", false);
  });

  it("opens the focused row on Enter while rows are picked", async () => {
    const rows = [conversation(5), conversation(4)];
    const router = await open(rows);
    await pickBox("Subject 5");

    row("Subject 4").element().focus();
    await userEvent.keyboard("{Enter}");

    await expect.poll(() => router.state.location.pathname).toBe(`/mail/inbox/${rows[1].id}`);
    await picked("Subject 5");
  });

  it("picks with Space, and Escape stops picking", async () => {
    await open([conversation(5), conversation(4)]);

    row("Subject 4").element().focus();
    await userEvent.keyboard(" ");
    await picked("Subject 4");
    await userEvent.keyboard("{Escape}");

    await picked("Subject 4", false);
    await expect.element(row("Subject 4")).toHaveFocus();
  });

  it("stops picking on Back, and takes back the entry it added when stopped another way", async () => {
    const router = await open([conversation(5), conversation(4)]);
    const entries = router.history.length;

    await pickBox("Subject 5");
    await expect.poll(() => router.history.length).toBe(entries + 1);
    router.history.back();
    await picked("Subject 5", false);
    expect(router.state.location.pathname).toBe("/mail/inbox");

    await pickBox("Subject 4");
    await expect.poll(() => router.history.location.state.picking).toBeDefined();
    await pickBox("Subject 4");
    await picked("Subject 4", false);
    await expect.poll(() => router.history.location.state.picking).toBeUndefined();
  });

  it("starts with none picked on coming back to a place", async () => {
    const router = await open([conversation(5), conversation(4, { inbox: false })]);
    await pickBox("Subject 5");
    await picked("Subject 5");

    await router.navigate({ to: "/mail/$place", params: { place: "all" } });
    await expect.element(row("Subject 4")).toBeVisible();
    await router.navigate({ to: "/mail/$place", params: { place: "inbox" } });

    await picked("Subject 5", false);
  });
});
