import { RouterProvider } from "@tanstack/react-router";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { cleanup, render } from "vitest-browser-react";
import { getListOutgoingMessagesQueryKey } from "@/api/generated/inbok";
import type { ListedAccount, OutgoingMessage } from "@/api/generated/model";
import { clearAccountCache } from "@/features/auth/session-cache";
import { appRouter, memberSession } from "@/test/app-router";
import { mailApi } from "@/test/mail-api";
import { noMessageOpen } from "./text";

const ok = 200;
const created = 201;
const notFound = 404;
const internalError = 500;

const studio: ListedAccount = {
  id: 7,
  provider: "gmail",
  email: "studio@example.com",
  name: "Studio",
  color: 1,
  status: "usable",
  createdAt: "2026-11-01T00:00:00Z",
  needsReconnectSince: null,
  allowedActions: ["sync", "send", "reconnect", "rename", "disconnect"],
  sync: {
    status: "complete",
    failure: null,
    failedAt: null,
    lastSyncedAt: "2026-11-13T08:00:00Z",
    messagesSoFar: null,
  },
};
const personal: ListedAccount = { ...studio, id: 8, email: "me@example.org", name: null, color: 3 };

// A message Gmail took, from Studio.
const sent: OutgoingMessage = {
  id: "019a0000-0000-7000-8000-0000000000a1",
  connectedAccountId: studio.id,
  from: studio.email,
  to: ["ana@example.com"],
  cc: [],
  bcc: [],
  subject: "Lunch",
  body: "Noon?",
  status: "sent",
  failure: null,
  createdAt: "2026-11-16T08:00:00Z",
  sentAt: "2026-11-16T08:00:05Z",
  sentAgainAs: null,
  outstanding: false,
  reply: false,
  allowedActions: [],
};
// A message Gmail refused, to two people and one more in Cc.
const refused: OutgoingMessage = {
  ...sent,
  id: "019a0000-0000-7000-8000-0000000000a2",
  to: ["budi@example.com", "citra@example.com"],
  cc: ["dewi@example.com"],
  subject: "Q4 deck",
  body: "The numbers changed.",
  status: "failed",
  failure: "refused",
  sentAt: null,
  outstanding: true,
  allowedActions: ["send-again"],
};
// A message that failed and was sent again, from the member's other account.
const tried: OutgoingMessage = {
  ...refused,
  id: "019a0000-0000-7000-8000-0000000000a3",
  connectedAccountId: personal.id,
  from: personal.email,
  to: ["hadi@example.com"],
  cc: [],
  subject: "Weekly update",
  failure: "gave-up",
  sentAgainAs: sent.id,
  outstanding: false,
};

// The Outbox's API as serve sets it: the lists its reads answer in turn,
// the last one repeating, or failing with outbox's status from the read
// failFrom names (counting from 0); the member's accounts; and the copy
// Send Message Again saves for the message again names.
type OutboxApi = {
  lists: OutgoingMessage[][];
  reads: number;
  failFrom: number;
  outbox: number;
  accounts: ListedAccount[];
  again?: { id: string; copy: OutgoingMessage };
};
let api: OutboxApi;

// serve sets the Outbox's API: its list holds messages, then each of later
// in turn; it fails with outbox from the read failFrom names (from the
// first by default, when outbox is a failure); the member has accounts;
// and again, when given, is what sending a message again saves.
function serve({
  messages = [sent, refused, tried],
  later = [],
  accounts = [studio, personal],
  outbox = ok,
  failFrom = 0,
  again,
}: {
  messages?: OutgoingMessage[];
  later?: OutgoingMessage[][];
  accounts?: ListedAccount[];
  outbox?: number;
  failFrom?: number;
  again?: { id: string; copy: OutgoingMessage };
} = {}): void {
  api = { lists: [messages, ...later], reads: 0, failFrom, outbox, accounts, again };
}

// problem returns an API problem answer of status.
function problem(status: number): Response {
  return Response.json(
    { type: "/api/v0/problems/internal-error", status, title: "", detail: "" },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

// sentAgainAs returns message once sent again as copy, no longer
// outstanding, as the API then says.
function sentAgainAs(message: OutgoingMessage, copy: OutgoingMessage): OutgoingMessage {
  return { ...message, sentAgainAs: copy.id, outstanding: false };
}

// answer answers the Outbox's requests from api, and nothing else.
function answer(url: string, init?: RequestInit): Promise<Response> | undefined {
  const path = new URL(url, location.href).pathname;
  const method = init?.method ?? "GET";
  if (path === "/api/v0/outgoing-messages" && method === "GET") {
    const read = api.reads++;
    if (api.outbox !== ok && read >= api.failFrom) return Promise.resolve(problem(api.outbox));
    const listed = api.lists[Math.min(read, api.lists.length - 1)];
    return Promise.resolve(Response.json({ data: { messages: listed } }, { status: ok }));
  }
  const again = api.again;
  if (again && method === "POST" && path === `/api/v0/outgoing-messages/${again.id}/send-again`) {
    // The copy joins the list, and the message points to it.
    const before = api.lists[Math.min(api.reads, api.lists.length - 1)];
    const after = before.map((one) => (one.id === again.id ? sentAgainAs(one, again.copy) : one));
    api.lists = [[again.copy, ...after]];
    api.reads = 0;
    return Promise.resolve(Response.json({ data: again.copy }, { status: created }));
  }
  if (path.startsWith("/api/v0/outgoing-messages/") && method === "GET") {
    const known = [...api.lists.flat(), ...(again ? [again.copy] : [])];
    const one = known.find((message) => path === `/api/v0/outgoing-messages/${message.id}`);
    return Promise.resolve(one ? Response.json({ data: one }, { status: ok }) : problem(notFound));
  }
  return undefined;
}

let current: Awaited<ReturnType<typeof appRouter>> | undefined;

// show renders the app at path, an Outbox address, through its real routes,
// at the window's width. It returns the router.
async function show(path = "/mail/outbox", width = 1280) {
  await page.viewport(width, 800);
  const mail = mailApi([], api.accounts);
  const router = await appRouter(path, memberSession, (url, init) => {
    // The session is the member's, as appRouter answers it.
    if (new URL(url, location.href).pathname === "/api/v0/sessions/current") return undefined;
    return answer(url, init) ?? mail.fetch(url, init);
  });
  await render(<RouterProvider router={router} />);
  current = router;
  return router;
}

afterEach(async () => {
  // Unmounted first, so no list starts reading again once its data ends.
  await cleanup();
  if (current) clearAccountCache(current.options.context.queryClient);
  current = undefined;
  vi.unstubAllGlobals();
});

describe("OutboxPage", () => {
  it("lists each message by its recipients, subject, and status", async () => {
    serve();
    await show();

    await expect.element(page.getByRole("heading", { name: "Outbox", level: 1 })).toHaveFocus();
    const list = page.getByRole("grid", { name: "Messages sent from inbok" });
    // Recipients by mail's rules for names: several by their address's name.
    await expect
      .element(list.getByRole("row", { name: /^To ana@example.com, Lunch, Sent, / }))
      .toBeVisible();
    await expect
      .element(list.getByRole("row", { name: /^To budi, citra, dewi, Q4 deck, Not sent/ }))
      .toBeVisible();
    await expect
      .element(list.getByText("· Gmail refused it. Edit and send it again."))
      .toBeVisible();
    // A message sent again asks nothing more, whatever its own status.
    await expect
      .element(list.getByRole("row", { name: /Weekly update, Tried again/ }))
      .toBeVisible();
    await expect.element(list.getByText("· You sent it again as a new message.")).toBeVisible();
    await expect.element(list.getByText(/inbok keeps up to 50 messages here/)).toBeVisible();
  });

  it("keeps an address left to right, and each of several recipients in its own direction", async () => {
    const arabic = { ...sent, id: "019a0000-0000-7000-8000-0000000000a4", to: ["نور@example.org"] };
    serve({ messages: [arabic, refused] });
    await show();

    const one = page.getByRole("row", { name: /نور@example.org/ });
    await expect.element(one.getByText("نور@example.org")).toHaveAttribute("dir", "ltr");
    const several = page.getByRole("row", { name: /Q4 deck/ });
    await expect.element(several.getByText("budi", { exact: true })).toBeVisible();
    expect(several.element().querySelectorAll("bdi").length).toBe(3);
  });

  it("names each row's account only while accounts mix", async () => {
    serve();
    await show();
    await expect.element(page.getByRole("row", { name: /Lunch, Sent, .*, Studio$/ })).toBeVisible();

    await cleanup();
    serve({ messages: [sent], accounts: [studio] });
    await show();
    await expect.element(page.getByRole("row", { name: /Lunch, Sent, [^,]+$/ })).toBeVisible();
  });

  it("opens a message in the reading pane, its status above it", async () => {
    serve();
    await show();
    await expect.element(page.getByText(noMessageOpen)).toBeVisible();

    await userEvent.click(page.getByRole("row", { name: /Q4 deck/ }));

    await expect.element(page.getByRole("heading", { name: "Q4 deck", level: 2 })).toHaveFocus();
    await expect
      .element(page.getByRole("row", { name: /Q4 deck/ }))
      .toHaveAttribute("aria-current", "page");
    const status = page.getByRole("region", { name: "Status" });
    await expect.element(status.getByText("Not sent", { exact: true })).toBeVisible();
    // Gmail would refuse it again, so it is edited instead.
    await expect.element(status.getByRole("button", { name: "Edit and send" })).toBeVisible();
    await expect
      .element(status.getByRole("button", { name: "Send again" }))
      .not.toBeInTheDocument();
    // Already in the Outbox, so no link to it.
    await expect
      .element(status.getByRole("link", { name: "Open in Outbox" }))
      .not.toBeInTheDocument();
    await expect.element(page.getByText("The numbers changed.")).toBeVisible();
    await expect
      .element(page.getByText("to budi@example.com, citra@example.com, dewi@example.com"))
      .toBeVisible();
  });

  it("lists the messages not sent yet first, under their heading, then the done ones", async () => {
    // In the API's order: outstanding first.
    serve({ messages: [refused, sent, tried] });
    await show();

    const list = page.getByRole("grid", { name: "Messages sent from inbok" });
    const notSent = list.getByRole("rowgroup", { name: "Not sent yet" });
    const done = list.getByRole("rowgroup", { name: "Done" });
    await expect.element(notSent.getByRole("row", { name: /Q4 deck, Not sent/ })).toBeVisible();
    await expect.element(done.getByRole("row", { name: /Lunch, Sent/ })).toBeVisible();
    await expect
      .element(done.getByRole("row", { name: /Weekly update, Tried again/ }))
      .toBeVisible();
    expect(
      notSent
        .getByRole("row")
        .elements()
        .some((row) => /Lunch/.test(row.textContent)),
    ).toBe(false);
    expect(list.getByRole("rowgroup").elements()).toEqual([notSent.element(), done.element()]);
    await expect
      .element(
        list.getByText(
          "inbok keeps up to 50 messages here, dropping the oldest done ones first. What you send from Gmail itself is in Sent, not here.",
        ),
      )
      .toBeVisible();
  });

  it("leaves out a section with no messages, heading and all", async () => {
    serve({ messages: [sent, tried] });
    await show();

    const list = page.getByRole("grid", { name: "Messages sent from inbok" });
    // A section's own element has no box: the list places its rows.
    await expect.element(list.getByRole("rowgroup", { name: "Done" })).toBeInTheDocument();
    expect(list.getByRole("rowgroup", { name: "Not sent yet" }).elements()).toHaveLength(0);
    expect(list.getByText("Not sent yet").elements()).toHaveLength(0);
  });

  it("moves a message to Done once it is sent, keeping it open", async () => {
    const sending: OutgoingMessage = {
      ...sent,
      status: "sending",
      sentAt: null,
      outstanding: true,
    };
    serve({ messages: [sending], later: [[sending], [sent]] });
    await show();
    const list = page.getByRole("grid", { name: "Messages sent from inbok" });
    await userEvent.click(
      list.getByRole("rowgroup", { name: "Not sent yet" }).getByRole("row", { name: /Lunch/ }),
    );

    await expect
      .element(
        list.getByRole("rowgroup", { name: "Done" }).getByRole("row", { name: /Lunch, Sent/ }),
        {
          timeout: 10_000,
        },
      )
      .toBeVisible();
    expect(list.getByRole("rowgroup", { name: "Not sent yet" }).elements()).toHaveLength(0);
    await expect
      .element(page.getByRole("region", { name: "Status" }).getByText("Sent to ana@example.com"))
      .toBeVisible();
  });

  it("keeps keyboard focus on a row as it moves to Done", async () => {
    const sending: OutgoingMessage = {
      ...sent,
      status: "sending",
      sentAt: null,
      outstanding: true,
    };
    serve({
      messages: [sending, tried],
      later: [
        [sending, tried],
        [sending, tried],
        [sent, tried],
      ],
    });
    await show();
    const list = page.getByRole("grid", { name: "Messages sent from inbok" });
    await expect
      .element(
        list.getByRole("rowgroup", { name: "Not sent yet" }).getByRole("row", { name: /Lunch/ }),
      )
      .toBeVisible();
    await userEvent.tab();
    await expect.element(list.getByRole("row", { name: /Lunch/ })).toHaveFocus();

    await expect
      .element(
        list.getByRole("rowgroup", { name: "Done" }).getByRole("row", { name: /Lunch, Sent/ }),
        {
          timeout: 10_000,
        },
      )
      .toHaveFocus();
  });

  it("shows a message's status in place as it opens, and raises it only once it changes", async () => {
    const sending: OutgoingMessage = {
      ...sent,
      status: "sending",
      sentAt: null,
      outstanding: true,
    };
    serve({ messages: [sending], later: [[sending], [sent]] });
    await show();
    await userEvent.click(page.getByRole("row", { name: /Lunch/ }));

    const status = page.getByRole("region", { name: "Status" });
    await expect.element(status.getByText("Sending to ana@example.com...")).toBeVisible();
    expect(status.element().querySelector(".animate-in")).toBeNull();
    // The Outbox, read again while it sends, says Gmail took it.
    await expect
      .element(status.getByText("Sent to ana@example.com"), { timeout: 6_000 })
      .toBeVisible();
    expect(status.element().querySelector(".animate-in")).not.toBeNull();
  });

  it("opens one message in place of another, and Send again's copy in its place", async () => {
    const gaveUp: OutgoingMessage = {
      ...refused,
      id: "019a0000-0000-7000-8000-0000000000a5",
      to: ["eko@example.com"],
      cc: [],
      subject: "Invoice",
      failure: "gave-up",
    };
    const copy: OutgoingMessage = {
      ...sent,
      id: "019a0000-0000-7000-8000-0000000000a6",
      subject: "Invoice",
    };
    serve({ messages: [sent, gaveUp], again: { id: gaveUp.id, copy } });
    const router = await show();

    await userEvent.click(page.getByRole("row", { name: /Lunch/ }));
    await userEvent.click(page.getByRole("row", { name: /Invoice/ }));
    await userEvent.click(
      page.getByRole("region", { name: "Status" }).getByRole("button", { name: "Send again" }),
    );

    await expect.element(page.getByText("Sent to ana@example.com")).toBeVisible();
    expect(router.state.location.pathname).toBe(`/mail/outbox/${copy.id}`);
    await expect.element(page.getByRole("row", { name: /Invoice, Tried again/ })).toBeVisible();
    // Each opened in the place of the one before, so one step back is the list.
    router.history.back();
    await expect.element(page.getByText(noMessageOpen)).toBeVisible();
    expect(router.state.location.pathname).toBe("/mail/outbox");
  });

  it("says a reread failed above an empty Outbox too", async () => {
    serve({ messages: [], outbox: internalError, failFrom: 1 });
    const router = await show();
    await expect.element(page.getByText("Nothing sent from inbok yet")).toBeVisible();

    await router.options.context.queryClient.invalidateQueries({
      queryKey: getListOutgoingMessagesQueryKey(),
    });
    await expect.element(page.getByText(/^Could not reload your messages/)).toBeVisible();
    await expect.element(page.getByText("Nothing sent from inbok yet")).toBeVisible();
  });

  it("says when nothing was sent from inbok yet", async () => {
    serve({ messages: [] });
    await show();
    await expect.element(page.getByText("Nothing sent from inbok yet")).toBeVisible();
  });

  it("says when the list can't be read, with Try again", async () => {
    serve({ outbox: internalError });
    await show();
    await expect.element(page.getByText(/^Could not load your messages\./)).toBeVisible();
    serve();
    await userEvent.click(page.getByRole("button", { name: "Try again" }));
    await expect.element(page.getByRole("row", { name: /Lunch/ })).toBeVisible();
  });

  describe("on a phone", () => {
    it("opens a message in place of the list, and Back returns to it", async () => {
      serve();
      const router = await show("/mail/outbox", 390);
      await userEvent.click(page.getByRole("row", { name: /Q4 deck/ }));

      await expect.element(page.getByRole("heading", { name: "Q4 deck", level: 2 })).toBeVisible();
      await expect.element(page.getByRole("grid")).not.toBeInTheDocument();
      await userEvent.click(page.getByRole("button", { name: "Back to list" }));

      await expect.element(page.getByRole("row", { name: /Q4 deck/ })).toBeVisible();
      // Back went back through history: the row added the message's entry.
      expect(router.history.canGoBack()).toBe(false);
    });

    it("keeps Back for a message that can't be read", async () => {
      serve();
      // No message has this ID, so its read answers 404.
      const router = await show("/mail/outbox/019a0000-0000-7000-8000-0000000000ff", 390);

      await expect.element(page.getByText(/^Could not load this message\./)).toBeVisible();
      await userEvent.click(page.getByRole("button", { name: "Back to list" }));
      await expect.element(page.getByRole("row", { name: /Q4 deck/ })).toBeVisible();
      expect(router.state.location.pathname).toBe("/mail/outbox");
    });

    it("keeps Back for an address that is no message's", async () => {
      serve();
      await show("/mail/outbox/not-a-message", 390);

      await expect.element(page.getByRole("alert")).toMatchTextContent(/isn't in your Outbox/);
      await userEvent.click(page.getByRole("button", { name: "Back to list" }));
      await expect.element(page.getByRole("row", { name: /Q4 deck/ })).toBeVisible();
    });

    it("goes to the Outbox from a message opened from elsewhere", async () => {
      serve();
      const router = await show(`/mail/outbox/${refused.id}`, 390);
      await userEvent.click(page.getByRole("button", { name: "Back to list" }));

      await expect.element(page.getByRole("row", { name: /Q4 deck/ })).toBeVisible();
      expect(router.state.location.pathname).toBe("/mail/outbox");
    });
  });
});
