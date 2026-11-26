import { RouterProvider } from "@tanstack/react-router";
import { describe, expect, it } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { getListOutgoingMessagesQueryKey } from "@/api/generated/amplop";
import type { OutgoingMessageSummary, PlaceCount } from "@/api/generated/model";
import { appRouter } from "@/test/app-router";
import { listedAccount, mailApi } from "@/test/mail-api";
import { data } from "@/test/server";

// message returns an Outbox message with status, sent again as the
// message sentAgainAs names, if any.
function message(
  status: OutgoingMessageSummary["status"],
  id: string,
  sentAgainAs: string | null = null,
): OutgoingMessageSummary {
  return {
    id,
    status,
    bcc: [],
    cc: [],
    to: ["bo@example.com"],
    connectedAccountId: 1,
    createdAt: "2026-11-13T08:00:00Z",
    failure: status === "failed" ? "gave-up" : null,
    from: "ana@example.com",
    sentAgainAs,
    dismissedAt: null,
    sentAt: status === "sent" ? "2026-11-13T08:00:05Z" : null,
    subject: "Lunch",
    // As the API says it: until it is sent, or sent again.
    outstanding: status !== "sent" && sentAgainAs === null,
    reply: false,
    allowedActions: status === "failed" || status === "unknown" ? ["send-again"] : [],
  };
}

// open renders the app at the Inbox, wide, so the mail sidebar is open,
// for a member with one account, whose Outbox read answers with messages
// and List Places with places. It returns the app's router.
async function open(messages: OutgoingMessageSummary[], places: PlaceCount[] = []) {
  const api = mailApi([], [listedAccount(1, "ana@example.com")]);
  api.places = places;
  api.server.on("GET", "/api/v0/outgoing-messages", () => data({ messages }));
  const router = await appRouter("/mail/inbox", api.server);
  await render(<RouterProvider router={router} />);
  return router;
}

// nav returns the mail sidebar.
function nav() {
  return page.getByRole("navigation", { name: "Mail" });
}

describe("MailNav", () => {
  it("counts the Outbox's messages not sent yet, not one sent again", async () => {
    const messages = [
      message("sent", "0193c8a0-0000-7000-8000-000000000001"),
      message("waiting", "0193c8a0-0000-7000-8000-000000000002"),
      message("failed", "0193c8a0-0000-7000-8000-000000000003"),
      message("unknown", "0193c8a0-0000-7000-8000-000000000004"),
      // Sent again as the first message, so it asks nothing more.
      message(
        "failed",
        "0193c8a0-0000-7000-8000-000000000005",
        "0193c8a0-0000-7000-8000-000000000001",
      ),
    ];
    await open(messages);
    await expect
      .element(nav().getByRole("link", { name: "Outbox 3 not sent" }))
      .toHaveAttribute("href", "/mail/outbox");
  });

  // Beside Sent, where its messages go, and above the accounts' labels,
  // so its count stays in sight.
  it("lists the Outbox among the views, after Sent", async () => {
    await open([]);
    await expect.element(nav().getByRole("link", { name: /^Outbox/ })).toBeVisible();

    const names = nav()
      .getByRole("link")
      .elements()
      .map((link) => link.textContent);
    const sent = names.indexOf("Sent");
    expect(names.slice(sent, sent + 3)).toEqual(["Sent", "Outbox", "All mail"]);
  });

  it("shows the Outbox without a count when every message is sent", async () => {
    const router = await open([message("sent", "0193c8a0-0000-7000-8000-000000000001")]);
    const client = router.options.context.queryClient;
    await expect
      .poll(() => client.getQueryState(getListOutgoingMessagesQueryKey())?.status)
      .toBe("success");
    // React draws the answer on a following frame.
    await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    const outbox = nav().getByRole("link", { name: /^Outbox/ });
    await expect.element(outbox).toHaveAccessibleName("Outbox");
    expect(outbox.element().querySelector("[data-slot=sidebar-menu-badge]")).toBeNull();
  });

  it("shows every account's unread Inbox count, in the weight of unread mail", async () => {
    await open(
      [],
      [
        { accountId: 1, place: "inbox", name: null, parent: null, unread: 12847 },
        { accountId: 2, place: "inbox", name: null, parent: null, unread: 2 },
        { accountId: 1, place: "spam", name: null, parent: null, unread: 9 },
      ],
    );

    const inbox = nav().getByRole("link", { name: "Inbox 12,849 unread" });
    await expect.element(inbox).toBeVisible();
    // DESIGN.md "Sidebar": unread is the name and count in 600 weight.
    await expect.element(inbox).toHaveStyle({ fontWeight: "600" });
    expect(
      getComputedStyle(nav().getByRole("link", { name: "Spam" }).element()).fontWeight,
    ).not.toBe("600");
    // Only the Inbox counts among the views.
    await expect.element(nav().getByRole("link", { name: "Spam" })).toBeVisible();
  });
});
