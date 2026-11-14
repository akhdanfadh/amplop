import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import {
  ConversationView,
  type ConversationAccount,
  type ConversationMessage,
} from "./conversation";
import type { MailAddress } from "./people";

const ok = 200;

const account: ConversationAccount = { name: "Work", email: "me@example.com", color: 3 };
const me: MailAddress = { name: "Me", address: "me@example.com" };
const ana: MailAddress = { name: "Ana Lima", address: "ana@example.com" };

// message returns message number n of a thread, from ana to the account,
// read, a day after the one before; extra sets its other fields.
function message(n: number, extra: Partial<ConversationMessage> = {}): ConversationMessage {
  return {
    id: `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`,
    from: [ana],
    to: [me],
    cc: [],
    replyTo: [],
    date: new Date(Date.UTC(2026, 9, n, 9)).toISOString(),
    preview: `Preview ${n}`,
    unread: false,
    ...extra,
  };
}

// thread returns count messages, the first oldest.
function thread(count: number): ConversationMessage[] {
  return Array.from({ length: count }, (_, i) => message(i + 1));
}

// The message IDs whose content the view asked for.
let read: string[] = [];
// Whether content reads answer that the session is gone.
let expired = false;

beforeEach(() => {
  read = [];
  expired = false;
  vi.stubGlobal(
    "fetch",
    vi.fn<(input: string) => Promise<Response>>(async (input) => {
      const id = /\/messages\/([^/]+)\/content/.exec(input)?.[1] ?? "";
      read.push(id);
      if (expired)
        return Response.json(
          { type: "/api/v0/problems/unauthenticated", status: 401, title: "", detail: "" },
          { status: 401, headers: { "Content-Type": "application/problem+json" } },
        );
      return Response.json(
        {
          data: {
            remote: "none",
            attachments: [],
            bodies: [
              {
                type: "text",
                partId: "0",
                text: `Body of ${id}`,
                clipped: false,
                url: `/api/v0/messages/${id}/parts/0`,
                remoteUrl: null,
              },
            ],
          },
        },
        { status: ok },
      );
    }),
  );
});

afterEach(async () => {
  vi.unstubAllGlobals();
  await page.viewport(414, 896);
});

// Harness holds the view in a box width pixels wide and 600 high, the
// pane.
function Harness({
  messages,
  width,
  slots,
}: {
  messages: ConversationMessage[];
  width: number;
  slots: Partial<Parameters<typeof ConversationView>[0]>;
}) {
  return (
    <div data-pane style={{ width, height: 600 }}>
      <ConversationView
        subject="Spring plan"
        account={account}
        messages={messages}
        onUnauthorized={() => {}}
        {...slots}
      />
    </div>
  );
}

// show draws the view of messages in Harness, inside a router, as a page
// places it, with what else is given.
async function show(
  messages: ConversationMessage[],
  width = 700,
  slots: Partial<Parameters<typeof ConversationView>[0]> = {},
) {
  await page.viewport(width + 100, 800);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const root = createRootRoute();
  const conversation = createRoute({
    getParentRoute: () => root,
    path: "/",
    component: () => (
      <QueryClientProvider client={client}>
        <Harness messages={messages} width={width} slots={slots} />
      </QueryClientProvider>
    ),
  });
  const router = createRouter({
    routeTree: root.addChildren([conversation]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  return render(<RouterProvider router={router} />);
}

// A message's own action, as another feature puts in its header.
const starAction = () => (
  <button type="button" aria-label="Star">
    *
  </button>
);

// crossTo sets the pane's width, as a window resizing would, without moving
// focus, and waits for focus to reach the action now drawn.
async function crossTo(width: string): Promise<void> {
  const before = document.activeElement;
  const pane = document.querySelector<HTMLElement>("[data-pane]");
  if (pane) pane.style.width = width;
  await expect
    .poll(() => {
      const active = document.activeElement;
      return active !== before && active instanceof HTMLElement && active.offsetParent !== null
        ? active.getAttribute("aria-label")
        : null;
    })
    .toBe("Star");
}

describe("ConversationView", () => {
  it("opens the newest and unread messages, and counts the folded middle", async () => {
    const messages = thread(12);
    messages[5] = message(6, { unread: true });
    await show(messages);

    await expect.element(page.getByText(`Body of ${messages[11].id}`)).toBeVisible();
    await expect.element(page.getByText(`Body of ${messages[5].id}`)).toBeVisible();
    // m2-m5 and m7-m9 fold; m1 and m10-m11 show as rows.
    await expect.element(page.getByRole("button", { name: "4 more messages" })).toBeVisible();
    await expect.element(page.getByRole("button", { name: "3 more messages" })).toBeVisible();
    expect(page.getByRole("button", { name: /Preview/ }).all()).toHaveLength(3);
    // Only open messages load their content.
    expect(new Set(read)).toEqual(new Set([messages[5].id, messages[11].id]));
  });

  it("shows the folded messages and moves focus to the first of them", async () => {
    const messages = thread(12);
    await show(messages);

    await userEvent.click(page.getByRole("button", { name: "8 more messages" }));

    expect(page.getByRole("button", { name: /Preview/ }).all()).toHaveLength(11);
    await expect.element(page.getByRole("button", { name: /Preview 2/ })).toHaveFocus();
  });

  it("opens a folded message, loading it, and folds it back", async () => {
    const messages = thread(3);
    await show(messages);
    const row = page.getByRole("button", { name: /Preview 1/ });

    await userEvent.click(row);

    await expect.element(page.getByText(`Body of ${messages[0].id}`)).toBeVisible();
    const fold = page.getByRole("button", { name: "Fold the message from Ana Lima" }).first();
    await expect.element(fold).toHaveFocus();

    await userEvent.keyboard("{Enter}");

    await expect.element(page.getByText(`Body of ${messages[0].id}`)).not.toBeInTheDocument();
    await expect.element(page.getByRole("button", { name: /Preview 1/ })).toHaveFocus();
  });

  it("calls out a Reply-To at another domain, not at the sender's own", async () => {
    await show([
      message(1, {
        unread: true,
        from: [{ name: "Committee", address: "committee@example.co.id" }],
        replyTo: [{ name: "Rina", address: "rina@gmail.com" }],
      }),
      message(2, {
        from: [{ name: "Newsletter", address: "news@example.co.id" }],
        replyTo: [{ name: "Help", address: "help@EXAMPLE.co.id" }],
      }),
    ]);

    await expect
      .element(page.getByText(/Replies go to rina@gmail\.com, not the sender\./))
      .toBeVisible();
    expect(page.getByText(/Replies go to/).all()).toHaveLength(1);
  });

  it("shows the details, listing ten addresses before Show all", async () => {
    const many = Array.from({ length: 12 }, (_, i) => ({
      name: `Member ${i + 1}`,
      address: `member${i + 1}@example.com`,
    }));
    await show([message(1, { to: [me, ...many] })]);
    const details = page.getByRole("button", { name: "Details" });

    await userEvent.click(details);

    await expect.element(details).toHaveAttribute("aria-expanded", "true");
    await expect.element(page.getByText("Received by")).toBeVisible();
    // Ten: the account, then members 1 to 9.
    await expect.element(page.getByText("<member9@example.com>")).toBeVisible();
    await expect.element(page.getByText("<member10@example.com>")).not.toBeInTheDocument();

    await userEvent.click(page.getByRole("button", { name: "Show all 13" }));

    await expect.element(page.getByText("<member12@example.com>")).toBeVisible();
  });

  it("names a message with no sender, no recipients, or a group", async () => {
    await show([
      message(1, { unread: true, from: [], to: [] }),
      message(2, {
        unread: true,
        from: [{ name: "news@example.com", address: "news@example.com" }],
        to: [{ name: "Undisclosed recipients", address: "" }],
      }),
    ]);

    await expect.element(page.getByText("Unknown sender")).toBeVisible();
    await expect.element(page.getByText("to no one")).toBeVisible();
    // A name that is its address shows once.
    expect(page.getByText("news@example.com").all()).toHaveLength(1);
    await expect.element(page.getByText("to Undisclosed recipients")).toBeVisible();
  });

  it("puts what other features build in its slots", async () => {
    const messages = thread(2);
    await show(messages, 700, {
      actions: <button type="button">Archive</button>,
      messageActions: (m) => <button type="button">{`Reply to ${m.preview}`}</button>,
    });

    await expect
      .element(page.getByRole("toolbar", { name: "Conversation actions" }).getByRole("button"))
      .toHaveTextContent("Archive");
    await expect.element(page.getByRole("button", { name: "Reply to Preview 2" })).toBeVisible();
    // A folded message shows no actions; its row opens it.
    await expect
      .element(page.getByRole("button", { name: "Reply to Preview 1" }))
      .not.toBeInTheDocument();
  });

  it("raises the toolbar once the title scrolls away", async () => {
    // Every message open, so the view is taller than its box.
    await show(Array.from({ length: 6 }, (_, i) => message(i + 1, { unread: true })));
    await expect.element(page.getByText(/Body of/).first()).toBeVisible();
    const bar = document.querySelector<HTMLElement>(".sticky");
    if (!bar?.parentElement) throw new Error("the view has no toolbar");
    expect(bar.dataset.raised).toBeUndefined();

    bar.parentElement.scrollTo({ top: 400 });

    // The subject shows in it, and a hairline below it.
    await expect.poll(() => bar.dataset.raised).toBe("true");
  });

  it("keeps a long address whole on its own line in a narrow pane", async () => {
    // Too long to share the name's line, short enough for one of its own.
    const long = "bart.fitzgerald@nw.example.com";
    await show([message(1, { from: [{ name: "Bartholomew Fitzgerald", address: long }] })], 390);
    const address = page.getByText(`<${long}>`).element();
    const name = page.getByText("Bartholomew Fitzgerald").element();

    // Below the name, and on one line.
    expect(address.getBoundingClientRect().top).toBeGreaterThan(
      name.getBoundingClientRect().bottom - 1,
    );
    expect(address.getBoundingClientRect().height).toBeLessThan(
      2 * Number.parseFloat(getComputedStyle(address).lineHeight),
    );
  });

  it("draws its avatars and details button at their sizes", async () => {
    await show([message(1)]);

    // Class merging keeps the view's sizes over the components' own.
    const fold = page.getByRole("button", { name: "Fold the message from Ana Lima" }).element();
    const avatar = fold.firstElementChild?.getBoundingClientRect();
    expect([avatar?.width, avatar?.height]).toEqual([36, 36]);
    const details = page.getByRole("button", { name: "Details" }).element().getBoundingClientRect();
    expect([details.width, details.height]).toEqual([20, 20]);
  });

  it("marks a folded message known to have attachments", async () => {
    await show([message(1, { attachments: true }), message(2), message(3)]);

    // The row's name says so.
    await expect
      .element(page.getByRole("button", { name: /Preview 1 Has attachments/ }))
      .toBeVisible();
    expect(page.getByText("Has attachments").all()).toHaveLength(1);
  });

  it("keeps a message folded again as its own row, with focus on it", async () => {
    const messages = thread(12);
    messages[5] = message(6, { unread: true });
    await show(messages);

    await userEvent.click(
      page.getByRole("button", { name: "Fold the message from Ana Lima" }).first(),
    );

    // Not back into the run around it.
    await expect.element(page.getByRole("button", { name: /Preview 6/ })).toHaveFocus();
    await expect.element(page.getByRole("button", { name: "4 more messages" })).toBeVisible();
    await expect.element(page.getByRole("button", { name: "3 more messages" })).toBeVisible();
  });

  it("says nothing of Reply-To on a message the account sent", async () => {
    // A sent message is answered to its To, not its Reply-To.
    await show([
      message(1, {
        from: [{ name: "Me", address: "me@example.com" }],
        replyTo: [{ name: "", address: "support@trusted.example" }],
      }),
    ]);

    await expect.element(page.getByText(/Replies go to/)).not.toBeInTheDocument();
  });

  it("shows every sender, so none can hide from the Reply-To check", async () => {
    await show([
      message(1, {
        from: [
          { name: "Payroll", address: "payroll@company.example" },
          { name: "", address: "helper@evil.example" },
        ],
      }),
    ]);

    await expect.element(page.getByText("<payroll@company.example>,")).toBeVisible();
    await expect.element(page.getByText("helper@evil.example", { exact: true })).toBeVisible();
  });

  it("reports a session gone while content loads", async () => {
    expired = true;
    const onUnauthorized = vi.fn<() => void>();
    await show([message(1)], 700, { onUnauthorized });

    await expect.poll(() => onUnauthorized.mock.calls.length).toBeGreaterThan(0);
  });

  it("moves focus to the first address Show all reveals", async () => {
    const many = Array.from({ length: 12 }, (_, i) => ({
      name: `Member ${i + 1}`,
      address: `member${i + 1}@example.com`,
    }));
    await show([message(1, { to: [me, ...many] })]);
    await userEvent.click(page.getByRole("button", { name: "Details" }));

    page.getByRole("button", { name: "Show all 13" }).element().focus();
    await userEvent.keyboard("{Enter}");

    // The eleventh: the account, members 1 to 9, then member 10.
    await expect.poll(() => document.activeElement?.textContent).toContain("member10@example.com");
  });

  it("keeps focus on a message's action as the pane crosses 512 px, both ways", async () => {
    await show([message(1)], 700, { messageActions: starAction });
    const replies = page.getByRole("button", { name: "Star" });
    await expect.element(replies.first()).toBeVisible();
    // The header's, drawn beside the date in a wide pane.
    const wide = replies
      .elements()
      .find((button) => button instanceof HTMLElement && button.offsetParent !== null);
    if (wide instanceof HTMLElement) wide.focus();

    // As a window narrowing and widening would, twice, without moving
    // focus.
    await crossTo("390px");
    await crossTo("700px");
    await crossTo("390px");
    await crossTo("700px");
  });

  it("leaves focus alone across 512 px once the member clicks away", async () => {
    await show([message(1)], 700, { messageActions: starAction });
    const replies = page.getByRole("button", { name: "Star" });
    await expect.element(replies.first()).toBeVisible();
    const wide = replies
      .elements()
      .find((button) => button instanceof HTMLElement && button.offsetParent !== null);
    if (wide instanceof HTMLElement) wide.focus();

    // Blank space, not focusable: focus goes to the page, by the member's
    // doing.
    await userEvent.click(page.getByText("1 message", { exact: true }));
    const pane = document.querySelector<HTMLElement>("[data-pane]");
    if (pane) pane.style.width = "390px";
    await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));

    expect(document.activeElement?.getAttribute("aria-label")).not.toBe("Star");
  });
});
