import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  RouterProvider,
  createMemoryHistory,
  createRootRouteWithContext,
  createRoute,
  createRouter,
} from "@tanstack/react-router";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { getListOutgoingMessagesQueryKey } from "@/api/generated/inbok";
import type { OutgoingMessageSummary, PlaceCount } from "@/api/generated/model";
import { Sidebar, SidebarContent, SidebarProvider } from "@/components/ui/sidebar";
import { Route as appRoot } from "@/routes/__root";
import { MailNav } from "./nav";

const ok = 200;

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
    sentAt: status === "sent" ? "2026-11-13T08:00:05Z" : null,
    subject: "Lunch",
  };
}

// app returns a router whose page is the mail sidebar, inside the app's
// root, whose Outbox read answers with messages and List Places with
// places, and the sidebar's queries.
function app(messages: OutgoingMessageSummary[], places: PlaceCount[] = []) {
  vi.stubGlobal(
    "fetch",
    vi.fn<(input: RequestInfo | URL) => Promise<Response>>(async (input) =>
      (input instanceof Request ? input.url : input.toString()).includes("/api/v0/places")
        ? Response.json({ data: { places } }, { status: ok })
        : Response.json({ data: { messages } }, { status: ok }),
    ),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const root = createRootRouteWithContext<{ queryClient: QueryClient }>()({
    component: appRoot.options.component,
  });
  const home = createRoute({
    getParentRoute: () => root,
    path: "/",
    component: () => (
      <QueryClientProvider client={client}>
        <SidebarProvider open onOpenChange={() => {}}>
          <Sidebar label="Mail">
            <SidebarContent>
              <MailNav />
            </SidebarContent>
          </Sidebar>
        </SidebarProvider>
      </QueryClientProvider>
    ),
  });
  const router = createRouter({
    routeTree: root.addChildren([home]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
    context: { queryClient: client },
  });
  return { router, client };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("MailNav", () => {
  it("counts the Outbox's messages not sent yet, not one sent again", async () => {
    await page.viewport(1440, 900);
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
    await render(<RouterProvider router={app(messages).router} />);
    await expect
      .element(page.getByRole("link", { name: "Outbox 3 not sent" }))
      .toHaveAttribute("href", "/mail/outbox");
  });

  it("shows the Outbox without a count when every message is sent", async () => {
    await page.viewport(1440, 900);
    const { router, client } = app([message("sent", "0193c8a0-0000-7000-8000-000000000001")]);
    await render(<RouterProvider router={router} />);
    await expect
      .poll(() => client.getQueryState(getListOutgoingMessagesQueryKey())?.status)
      .toBe("success");
    // React draws the answer on a following frame.
    await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    const outbox = page.getByRole("link", { name: /^Outbox/ });
    await expect.element(outbox).toHaveAccessibleName("Outbox");
    expect(outbox.element().querySelector("[data-slot=sidebar-menu-badge]")).toBeNull();
  });

  it("shows every account's unread Inbox count, in the weight of unread mail", async () => {
    await page.viewport(1440, 900);
    const { router } = app(
      [],
      [
        { accountId: 1, place: "inbox", name: null, parent: null, unread: 12847 },
        { accountId: 2, place: "inbox", name: null, parent: null, unread: 2 },
        { accountId: 1, place: "spam", name: null, parent: null, unread: 9 },
      ],
    );
    await render(<RouterProvider router={router} />);

    const inbox = page.getByRole("link", { name: "Inbox 12,849 unread" });
    await expect.element(inbox).toBeVisible();
    await expect.element(inbox).toHaveClass(/font-semibold/);
    // Only the Inbox counts among the views.
    await expect.element(page.getByRole("link", { name: "Spam" })).toBeVisible();
  });
});
