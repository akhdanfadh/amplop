import { RouterProvider } from "@tanstack/react-router";
import { afterEach, describe, expect, it } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { cleanup, render } from "vitest-browser-react";
import { clearAccountCache } from "@/features/auth/session-cache";
import { appRouter } from "@/test/app-router";
import { conversation, listedAccount, mailApi } from "@/test/mail-api";
import { memberSession } from "@/test/server";
import { reminderText, remindersLabel } from "./text";

// A phone's window width, where the frame has a top bar.
const phoneWidth = 390;

let current: Awaited<ReturnType<typeof appRouter>> | undefined;

afterEach(async () => {
  await cleanup();
  if (current) clearAccountCache(current.options.context.queryClient);
  current = undefined;
});

describe("the setup reminders in the app", () => {
  it("stack under a phone's top bar, above the page, within its width", async () => {
    await page.viewport(phoneWidth, 800);
    const api = mailApi([conversation(1)], [listedAccount(1, "ana@example.com")]);
    api.server.session = { ...memberSession, emailVerified: false };
    current = await appRouter("/mail/inbox", api.server);
    await render(<RouterProvider router={current} />);

    const reminders = page.getByRole("region", { name: remindersLabel });
    await expect
      .element(reminders.getByText(reminderText.verifyEmail.notice, { exact: false }))
      .toBeVisible();
    await expect
      .element(reminders.getByRole("link", { name: reminderText.verifyEmail.link }))
      .toBeVisible();
    // On a phone the place's heading sits in the top bar, the page's own bar.
    const heading = page.getByRole("heading", { level: 1, name: "Inbox" });
    await expect.element(heading).toBeVisible();
    const topBar = heading.element().closest("header");
    expect(topBar).not.toBeNull();

    // Top bar, then the reminders, then the page, each below the last.
    const section = reminders.element().getBoundingClientRect();
    const content = reminders.element().nextElementSibling?.getBoundingClientRect();
    expect(topBar!.getBoundingClientRect().bottom).toBeLessThanOrEqual(section.top);
    expect(section.bottom).toBeLessThanOrEqual(content!.top);
    // The banner wraps within the window rather than widening the page.
    expect(section.right).toBeLessThanOrEqual(phoneWidth);
    expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(phoneWidth);
  });
});
