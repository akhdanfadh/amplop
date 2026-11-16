import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import {
  ConversationView,
  type ConversationAccount,
  type ConversationMessage,
} from "./conversation";
import type { ListedAccount } from "@/api/generated/model";
import type { MailAddress } from "@/features/outgoing-messages/reply-defaults";

const ok = 200;

const account: ConversationAccount = { id: 7, name: "Work", email: "me@example.com", color: 3 };
// The account as the accounts read lists it, for the reply editor.
const listed: ListedAccount = {
  id: account.id,
  provider: "gmail",
  email: account.email,
  name: account.name,
  color: account.color,
  status: "usable",
  createdAt: "2026-11-01T00:00:00Z",
  needsReconnectSince: null,
  sync: {
    status: "complete",
    failure: null,
    failedAt: null,
    lastSyncedAt: "2026-11-13T08:00:00Z",
    messagesSoFar: null,
  },
};
const me: MailAddress = { name: "Me", address: "me@example.com" };
const ana: MailAddress = { name: "Ana Lima", address: "ana@example.com" };

// message returns message number n of a thread, from ana to the account,
// read, a day after the one before, answering none; extra sets its other
// fields.
function message(n: number, extra: Partial<ConversationMessage> = {}): ConversationMessage {
  return {
    id: messageId(n),
    subject: "Spring plan",
    from: [ana],
    to: [me],
    cc: [],
    replyTo: [],
    date: new Date(Date.UTC(2026, 9, n, 9)).toISOString(),
    preview: `Preview ${n}`,
    unread: false,
    starred: false,
    spam: false,
    trash: false,
    sent: false,
    labelIds: [],
    answers: null,
    attachments: false,
    ...extra,
  };
}

// messageId returns the ID of message number n.
function messageId(n: number): string {
  return `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
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
      if (input === "/api/v0/connected-accounts")
        return Response.json({ data: { accounts: [listed], syncStopped: false } }, { status: ok });
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
// pane, with a button that removes the first message, as a sync would,
// and one that starts the view at nextStart, as a new address would.
function Harness({
  messages: initial,
  width,
  slots,
  nextStart,
}: {
  messages: ConversationMessage[];
  width: number;
  slots: Partial<Parameters<typeof ConversationView>[0]>;
  nextStart?: string;
}) {
  const [messages, setMessages] = useState(initial);
  const [startAt, setStartAt] = useState(slots.startAt);
  return (
    <>
      <div data-pane style={{ width, height: 600 }}>
        <ConversationView
          subject="Spring plan"
          account={account}
          messages={messages}
          onUnauthorized={() => {}}
          {...slots}
          startAt={startAt}
        />
      </div>
      <button type="button" onClick={() => setMessages((was) => was.slice(1))}>
        Sync removes the first message
      </button>
      <button type="button" onClick={() => setStartAt(nextStart)}>
        Start at another message
      </button>
    </>
  );
}

// show draws the view of messages in Harness, inside a router, as a page
// places it, with what else is given.
async function show(
  messages: ConversationMessage[],
  width = 700,
  slots: Partial<Parameters<typeof ConversationView>[0]> = {},
  nextStart?: string,
) {
  await page.viewport(width + 100, 800);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const root = createRootRoute();
  const conversation = createRoute({
    getParentRoute: () => root,
    path: "/",
    component: () => (
      <QueryClientProvider client={client}>
        <Harness messages={messages} width={width} slots={slots} nextStart={nextStart} />
      </QueryClientProvider>
    ),
  });
  const router = createRouter({
    routeTree: root.addChildren([conversation]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  return render(<RouterProvider router={router} />);
}

// middle returns how far down the window element's middle is.
function middle(element: Element): number {
  const box = element.getBoundingClientRect();
  return box.top + box.height / 2;
}

// firstAnaRow returns the row of the first open message from Ana.
function firstAnaRow(): Element | null {
  return page
    .getByRole("article", { name: /^Ana Lima, / })
    .first()
    .element()
    .closest("li");
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

  it("opens the message it starts at, and another it is told to start at", async () => {
    const messages = thread(12);
    await show(messages, 700, { startAt: messages[4].id }, messages[5].id);
    await expect.element(page.getByText(`Body of ${messages[4].id}`)).toBeVisible();
    await expect.element(page.getByText(`Body of ${messages[5].id}`)).not.toBeInTheDocument();

    // The conversation stays open; the address names another message.
    await userEvent.click(page.getByRole("button", { name: "Start at another message" }));

    await expect.element(page.getByText(`Body of ${messages[5].id}`)).toBeVisible();
    // The first stays open too.
    await expect.element(page.getByText(`Body of ${messages[4].id}`)).toBeVisible();
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
      back: <button type="button">Back</button>,
      actions: <button type="button">Archive</button>,
      messageActions: (m) => <button type="button">{`Reply to ${m.preview}`}</button>,
    });

    await expect
      .element(page.getByRole("toolbar", { name: "Conversation actions" }).getByRole("button"))
      .toHaveTextContent("Archive");
    // Back leads the toolbar, before the actions.
    await expect.element(page.getByRole("button", { name: "Back" })).toBeVisible();
    await expect.element(page.getByRole("button", { name: "Reply to Preview 2" })).toBeVisible();
    // After the message's own Reply.
    await expect
      .element(page.getByRole("button", { name: "Reply", exact: true }).first())
      .toBeVisible();
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

  it("ends with the reply editor, answering the newest message", async () => {
    const messages = [
      message(1),
      message(2, { from: [{ name: "Ben", address: "ben@example.com" }], to: [me, ana] }),
    ];
    await show(messages);

    // Ben's message also reached Ana, whom Reply leaves out.
    // The bottom's, after the last message.
    await userEvent.click(page.getByRole("button", { name: "Reply all", exact: true }).last());

    const editor = page.getByRole("form");
    await expect.element(editor).toBeVisible();
    await expect.element(editor.getByText("ben@example.com")).toBeVisible();
    await expect.element(editor.getByText("ana@example.com")).toBeVisible();
    // Below the newest message, the last thing in the view.
    const newest = page.getByRole("article", { name: /Ben/ }).element();
    expect(editor.element().getBoundingClientRect().top).toBeGreaterThan(
      newest.getBoundingClientRect().bottom - 1,
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

  it("opens the editor below an older message from its own Reply", async () => {
    const messages = [
      message(1, { unread: true, from: [{ name: "Ben", address: "ben@example.com" }] }),
      message(2),
    ];
    await show(messages);
    const ben = page.getByRole("article", { name: /Ben/ });

    await userEvent.click(ben.getByRole("button", { name: "Reply", exact: true }).first());

    const editor = page.getByRole("form", { name: "Reply" });
    await expect.element(editor.getByText("ben@example.com")).toBeVisible();
    // Below Ben's message, above the newest.
    const newest = page.getByRole("article", { name: /Ana Lima/ }).element();
    expect(editor.element().getBoundingClientRect().bottom).toBeLessThan(
      newest.getBoundingClientRect().top + 1,
    );
    // One editor: every Reply hides until it closes.
    await expect
      .element(page.getByRole("button", { name: "Reply", exact: true }))
      .not.toBeInTheDocument();

    await userEvent.click(editor.getByRole("button", { name: "Discard" }));

    await expect
      .element(ben.getByRole("button", { name: "Reply", exact: true }).first())
      .toHaveFocus();
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

  it("keeps the editor and its text when sync removes the message answered", async () => {
    const messages = [
      message(1, { unread: true, from: [{ name: "Ben", address: "ben@example.com" }] }),
      message(2),
    ];
    await show(messages);
    await userEvent.click(
      page
        .getByRole("article", { name: /Ben/ })
        .getByRole("button", { name: "Reply", exact: true })
        .first(),
    );
    await userEvent.type(page.getByRole("textbox", { name: "Message" }), "Noon works.");

    await userEvent.click(page.getByRole("button", { name: "Sync removes the first message" }));

    await expect
      .element(page.getByText("The message you're answering was deleted.", { exact: true }).first())
      .toBeVisible();
    // Announced, as it comes while the member types.
    await expect
      .element(page.getByRole("status"))
      .toHaveTextContent("The message you're answering was deleted.");
    await expect.element(page.getByRole("textbox", { name: "Message" })).toHaveValue("Noon works.");
  });

  it("says nothing of Reply-To on a message the account sent, from an alias too", async () => {
    // A sent message is answered to its To, not its Reply-To.
    await show([
      message(1, {
        from: [{ name: "Me", address: "me@alias.example" }],
        replyTo: [{ name: "", address: "support@trusted.example" }],
        sent: true,
      }),
    ]);

    await expect.element(page.getByText(/Replies go to/)).not.toBeInTheDocument();
  });

  it("calls out Reply-To on a message from the account's address that it did not send", async () => {
    // Anyone can write the account's address in From.
    await show([
      message(1, {
        from: [{ name: "Me", address: "me@example.com" }],
        replyTo: [{ name: "", address: "support@evil.example" }],
      }),
    ]);

    await expect.element(page.getByText(/Replies go to/)).toBeVisible();
  });

  it("names an earlier message a message answers, and shows it", async () => {
    // The newest answers the second, which "2 more messages" holds; the
    // unread messages after them start open, so the pane can scroll the
    // second to its top.
    const messages = thread(12);
    for (const later of messages.slice(3)) later.unread = true;
    messages[11] = { ...messages[11], answers: messageId(2) };
    await show(messages);

    await userEvent.click(page.getByRole("button", { name: /^In reply to Ana Lima on / }));

    // Open, at the pane's top under the toolbar, with focus on the message
    // itself, so a second Enter does not fold it.
    const shown = page.getByRole("article", { name: /^Ana Lima, / }).first();
    await expect
      .element(page.getByText("Body of 00000000-0000-4000-8000-000000000002"))
      .toBeVisible();
    await expect.element(shown).toHaveFocus();
    const pane = document.querySelector("[data-pane]")?.getBoundingClientRect().top ?? 0;
    const row = shown.element().closest("li")?.getBoundingClientRect().top ?? 0;
    expect(Math.round(row - pane)).toBe(48);
    await userEvent.keyboard("{Enter}");
    await expect
      .element(page.getByText("Body of 00000000-0000-4000-8000-000000000002"))
      .toBeVisible();
    // The third, alone now, takes its own row.
    await expect
      .element(page.getByRole("button", { name: /more messages/ }))
      .not.toBeInTheDocument();
  });

  it("wraps the line naming the message answered within a narrow pane", async () => {
    const billing: MailAddress = {
      name: "",
      address: "first.last+billing-notifications@example.com",
    };
    await show(
      [message(1, { from: [billing] }), message(2), message(3, { answers: messageId(1) })],
      360,
    );

    const line = page.getByRole("button", { name: /^In reply to first\.last/ });
    await expect.element(line).toBeVisible();
    const pane = document.querySelector("[data-pane]")?.getBoundingClientRect();
    expect(line.element().getBoundingClientRect().right).toBeLessThanOrEqual(pane?.right ?? 0);
  });

  it("opens a message the line names even when its row was already in sight", async () => {
    await show([message(1), message(2), message(3, { answers: messageId(1) })]);

    await userEvent.click(page.getByRole("button", { name: /^In reply to Ana Lima on / }));

    await expect
      .element(page.getByText("Body of 00000000-0000-4000-8000-000000000001"))
      .toBeVisible();
    // It flashes, so the press shows even with the message in sight.
    const row = page
      .getByRole("button", { name: "Fold the message from Ana Lima" })
      .first()
      .element()
      .closest("li");
    expect(row && getComputedStyle(row).animationName).toBe("flash-selection");
  });

  it("keeps the toolbar's faded subject from taking the pointer or a selection", async () => {
    await show(thread(2));

    const copy = document.querySelector("[data-raised], .sticky")?.querySelector("p[aria-hidden]");
    expect(copy && getComputedStyle(copy).pointerEvents).toBe("none");
    expect(copy && getComputedStyle(copy).userSelect).toBe("none");
  });

  it("names the message answered when the one just before is folded away", async () => {
    // The fourth answers the third, which "2 more messages" holds.
    const messages = thread(6);
    messages[3] = message(4, { unread: true, answers: messageId(3) });
    await show(messages);

    await expect.element(page.getByRole("button", { name: "2 more messages" })).toBeVisible();
    await expect
      .element(page.getByRole("button", { name: /^In reply to Ana Lima on / }))
      .toBeVisible();
  });

  it("keeps a right-to-left name apart from the date after it", async () => {
    const noor: MailAddress = { name: "نور الهدى", address: "noor@example.com" };
    await show([message(1, { from: [noor] }), message(2), message(3, { answers: messageId(1) })]);

    const line = page.getByRole("button", { name: /^In reply to / });
    await expect.element(line).toBeVisible();
    expect(line.element().querySelector("bdi")?.textContent).toBe("نور الهدى");
  });

  it("flashes the message again on a second press", async () => {
    await show([message(1), message(2), message(3, { answers: messageId(1) })]);
    const line = page.getByRole("button", { name: /^In reply to Ana Lima on / });

    await userEvent.click(line);
    await expect.poll(() => firstAnaRow()?.getAnimations().length).toBe(1);
    await new Promise((done) => setTimeout(done, 600));
    await userEvent.click(line);

    await expect
      .poll(() => Number(firstAnaRow()?.getAnimations()[0]?.currentTime ?? 9999))
      .toBeLessThan(400);
  });

  it("says nothing of the message answered when it is the one just before", async () => {
    await show([message(1), message(2, { answers: messageId(1) })]);

    await expect.element(page.getByRole("heading", { name: "Spring plan" })).toBeVisible();
    expect(page.getByText(/In reply to/).elements()).toHaveLength(0);
  });

  it("shows the title and the placeholder until the messages come, and takes focus", async () => {
    await show(thread(2), 700, {
      messages: undefined,
      placeholder: <p>Loading messages...</p>,
    });

    await expect.element(page.getByRole("heading", { name: "Spring plan" })).toHaveFocus();
    await expect.element(page.getByText("Loading messages...")).toBeVisible();
    // No count before the messages come.
    expect(page.getByText(/^\d+ messages?$/).elements()).toHaveLength(0);
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

  it("keeps a message's Reply at the end of the sender's line, wide or narrow", async () => {
    await show([message(1)]);
    const reply = page.getByRole("button", { name: "Reply", exact: true }).first();
    // The header's Reply shows once the member's accounts are read.
    await expect.element(reply).toBeVisible();
    const sender = page.getByText("Ana Lima", { exact: true }).first();
    const button = reply.element();
    if (button instanceof HTMLElement) button.focus();

    // One Reply, on the sender's line, wide and then narrow, where it keeps
    // focus.
    expect(Math.abs(middle(button) - middle(sender.element()))).toBeLessThanOrEqual(2);
    const pane = document.querySelector<HTMLElement>("[data-pane]");
    if (pane) pane.style.width = "390px";
    await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));

    expect(page.getByRole("button", { name: "Reply", exact: true }).elements()).toHaveLength(2);
    expect(Math.abs(middle(button) - middle(sender.element()))).toBeLessThanOrEqual(2);
    expect(document.activeElement).toBe(button);
  });
});
