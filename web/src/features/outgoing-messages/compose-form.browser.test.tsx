import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import {
  getListConnectedAccountsQueryKey,
  getListOutgoingMessagesQueryKey,
} from "@/api/generated/inbok";
import type { ListedAccount, OutgoingMessage } from "@/api/generated/model";
import { listedAccount } from "@/test/mail-api";
import { ComposeForm } from "./compose-form";
import { composeValues, type ComposeValues } from "./compose-schema";
import type { LeaveState } from "./text";

const ok = 200;
const created = 201;
const notFound = 404;
const unprocessable = 422;

const studio: ListedAccount = { ...listedAccount(7, "studio@example.com", 1), name: "Studio" };
const stale: ListedAccount = {
  ...studio,
  id: 8,
  email: "old@example.org",
  name: null,
  status: "needs-reconnect",
  allowedActions: ["reconnect", "rename", "disconnect"],
};

// The message the server saves.
const message: OutgoingMessage = {
  id: "019a0000-0000-7000-8000-0000000000bb",
  status: "waiting",
  failure: null,
  from: studio.email,
  connectedAccountId: studio.id,
  to: ["ana@example.com"],
  cc: [],
  bcc: [],
  subject: "Lunch",
  body: "Noon?",
  createdAt: "2026-11-16T08:00:00Z",
  sentAt: null,
  sentAgainAs: null,
  reply: false,
  allowedActions: [],
};

type Call = { method: string; url: string; headers: Headers; body: unknown };

// serve stubs the API: Send Message answers with each of answers in turn,
// the last one repeating. It returns the calls the form made.
function serve(
  answers: (() => Response)[] = [() => Response.json({ data: message }, { status: created })],
): Call[] {
  const calls: Call[] = [];
  let sends = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn<(url: string, init?: RequestInit) => Promise<Response>>(async (url, init = {}) => {
      const method = init.method ?? "GET";
      const body = typeof init.body === "string" ? JSON.parse(init.body) : undefined;
      calls.push({ method, url, headers: new Headers(init.headers), body });
      if (method === "POST" && url === "/api/v0/outgoing-messages") {
        const answer = answers[Math.min(sends, answers.length - 1)];
        sends++;
        return answer();
      }
      if (url === "/api/v0/outgoing-messages") return Response.json({}, { status: ok });
      return Response.json({}, { status: notFound });
    }),
  );
  return calls;
}

// show renders the compose form starting with initial, sending from one of
// accounts, as the docked window draws it, inside a router with a second page, /elsewhere, to
// leave for. It returns the messages the form reported sent, how many 401s
// it reported, what closing would lose as it last reported, the query
// client, the router, and the rendered screen.
async function show(
  initial: ComposeValues = composeValues([]),
  accounts: ListedAccount[] = [studio, stale],
) {
  const sent: OutgoingMessage[] = [];
  const unauthorized = { count: 0 };
  const loses: { last: LeaveState | null } = { last: null };
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const root = createRootRoute();
  const compose = createRoute({
    getParentRoute: () => root,
    path: "/",
    component: () => (
      <ComposeForm
        accounts={accounts}
        initial={initial}
        look="window"
        header={({ title }) => <h2>{title}</h2>}
        collapsed={false}
        onSent={(saved) => sent.push(saved)}
        onDiscard={() => {}}
        onLoses={(state) => {
          loses.last = state;
        }}
        onUnauthorized={() => unauthorized.count++}
      />
    ),
  });
  const elsewhere = createRoute({
    getParentRoute: () => root,
    path: "/elsewhere",
    component: () => <h1>Elsewhere</h1>,
  });
  const router = createRouter({
    routeTree: root.addChildren([compose, elsewhere]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  const screen = await render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return { sent, unauthorized, loses, client, router, screen };
}

// write chooses Studio in From and fills To, Subject, and Message.
async function write(): Promise<void> {
  await userEvent.click(page.getByRole("button", { name: /From/ }));
  await userEvent.click(page.getByRole("option", { name: "studio@example.com Studio" }));
  await userEvent.type(page.getByRole("textbox", { name: "To" }), "ana@example.com,");
  await userEvent.type(page.getByRole("textbox", { name: "Subject" }), "Lunch");
  await userEvent.type(page.getByRole("textbox", { name: "Message" }), "Noon?");
}

// problem returns an API problem response with status and type.
function problem(status: number, type: string): Response {
  return Response.json(
    { type, status, title: "Problem", detail: "The server's detail." },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

// lost is an answer that says nothing of whether the message was saved.
function lost(): Response {
  return problem(500, "/api/v0/problems/internal-error");
}

// posts returns the Send Message calls among calls.
function posts(calls: Call[]): Call[] {
  return calls.filter((call) => call.method === "POST");
}

// keyReused is Send Message's refusal of a key that saved another message.
function keyReused(): Response {
  return Response.json(
    {
      type: "/api/v0/problems/idempotency-key-reused",
      status: unprocessable,
      title: "Idempotency key reused",
      detail: "The key saved another message.",
    },
    { status: unprocessable, headers: { "Content-Type": "application/problem+json" } },
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("ComposeForm", () => {
  it("shows the message editor's rows, with Cc and Bcc on demand", async () => {
    serve();
    await show();

    const form = page.getByRole("form", { name: "New message" });
    await expect
      .element(form.getByRole("button", { name: /From/ }))
      .toHaveTextContent("Choose an account");
    await expect.element(form.getByRole("textbox", { name: "To" })).toBeVisible();
    await expect.element(form.getByRole("textbox", { name: "Cc" })).not.toBeInTheDocument();
    await expect.element(form.getByRole("textbox", { name: "Subject" })).toBeVisible();
    await expect.element(form.getByRole("textbox", { name: "Message" })).toBeVisible();

    await userEvent.click(form.getByRole("button", { name: "Add Cc recipients" }));
    await expect.element(form.getByRole("textbox", { name: "Cc" })).toHaveFocus();
  });

  it("shows the Cc and Bcc an edited message starts with", async () => {
    serve();
    await show(composeValues([studio.id], { ...message, cc: ["ben@example.com"] }));

    await expect
      .element(page.getByRole("grid", { name: "Cc" }).getByRole("row"))
      .toHaveTextContent("ben@example.com");
    await expect.element(page.getByRole("textbox", { name: "Bcc" })).not.toBeInTheDocument();
  });

  it("offers an account that needs reconnecting, but not to choose", async () => {
    serve();
    await show();

    await userEvent.click(page.getByRole("button", { name: /From/ }));
    await expect
      .element(page.getByRole("option", { name: "old@example.org (needs reconnecting)" }))
      .toHaveAttribute("aria-disabled", "true");
  });

  it("offers a usable account the server doesn't list able to send, but not to choose", async () => {
    serve();
    // As when no Google client is set up: usable, yet without send.
    await show(composeValues([]), [{ ...studio, allowedActions: ["rename", "disconnect"] }]);

    await userEvent.click(page.getByRole("button", { name: /From/ }));
    await expect
      .element(page.getByRole("option", { name: "studio@example.com Studio" }))
      .toHaveAttribute("aria-disabled", "true");
  });

  it("shows errors only after Send, with focus on the first", async () => {
    const calls = serve();
    await show();

    await expect
      .element(page.getByText("Choose the account to send from."))
      .not.toBeInTheDocument();
    await userEvent.click(page.getByRole("button", { name: "Send" }));

    await expect.element(page.getByText("Choose the account to send from.")).toBeVisible();
    await expect.element(page.getByText("Add at least one recipient.")).toBeVisible();
    await expect.element(page.getByRole("button", { name: /From/ })).toHaveFocus();
    expect(calls.filter((call) => call.method === "POST")).toHaveLength(0);
  });

  it("sends with Ctrl+Enter from any field, with a key, and reports the saved message", async () => {
    const calls = serve();
    const { sent } = await show();

    await write();
    await userEvent.click(page.getByRole("textbox", { name: "Subject" }));
    await userEvent.keyboard("{Control>}{Enter}{/Control}");

    await expect.poll(() => sent).toEqual([message]);
    const post = calls.find((call) => call.method === "POST");
    expect(post?.body).toEqual({
      connectedAccountId: studio.id,
      to: ["ana@example.com"],
      cc: [],
      bcc: [],
      subject: "Lunch",
      body: "Noon?",
    });
    expect(post?.headers.get("Idempotency-Key")).toMatch(/^"[0-9a-f-]{36}"$/);
  });

  it("locks the editor while it sends", async () => {
    // The send waits for an answer that never comes.
    vi.stubGlobal(
      "fetch",
      vi.fn<() => Promise<Response>>(() => new Promise(() => {})),
    );
    await show();
    await write();

    await userEvent.click(page.getByRole("button", { name: "Send" }));

    await expect.element(page.getByRole("button", { name: /Sending/ })).toHaveFocus();
    await expect.element(page.getByRole("textbox", { name: "Message" })).toBeDisabled();
    await expect.element(page.getByRole("textbox", { name: "To" })).toHaveAttribute("readonly");
  });

  it("sends a new message with a new key after the key saved another", async () => {
    const calls = serve([keyReused, () => Response.json({ data: message }, { status: created })]);
    const { sent } = await show();
    await write();

    await userEvent.click(page.getByRole("button", { name: "Send" }));
    await expect.element(page.getByRole("alert")).toMatchTextContent("Check the Outbox");
    await expect
      .element(page.getByRole("link", { name: "Open the Outbox in a new tab" }))
      .toBeVisible();

    await userEvent.click(page.getByRole("button", { name: "Send" }));
    await expect.poll(() => sent).toEqual([message]);
    const keys = calls
      .filter((call) => call.method === "POST")
      .map((call) => call.headers.get("Idempotency-Key"));
    expect(keys).toHaveLength(2);
    expect(keys[0]).not.toBe(keys[1]);
  });

  it("repeats a lost send with the same key and content, by itself and by hand", async () => {
    const calls = serve([
      () => problem(502, "/api/v0/problems/bad-gateway"),
      lost,
      () => Response.json({ data: message }, { status: created }),
    ]);
    const { sent } = await show();
    await write();

    await userEvent.click(page.getByRole("button", { name: "Send" }));
    // The 502 is repeated by itself after a second; the 500 is not.
    await expect
      .element(page.getByRole("alert"), { timeout: 5_000 })
      .toMatchTextContent("Sending again is safe");
    await userEvent.click(page.getByRole("button", { name: "Send" }));

    await expect.poll(() => sent).toEqual([message]);
    const sends = posts(calls);
    expect(sends).toHaveLength(3);
    expect(new Set(sends.map((call) => call.headers.get("Idempotency-Key"))).size).toBe(1);
    expect(sends[2].body).toEqual(sends[0].body);
  });

  it("keeps the key through edits after a lost send, so the server decides", async () => {
    const calls = serve([lost, () => Response.json({ data: message }, { status: created })]);
    await show();
    await write();

    await userEvent.click(page.getByRole("button", { name: "Send" }));
    await expect.element(page.getByRole("alert")).toMatchTextContent("Sending again is safe");
    await userEvent.type(page.getByRole("textbox", { name: "Subject" }), " today");
    await userEvent.click(page.getByRole("button", { name: "Send" }));

    await expect.poll(() => posts(calls)).toHaveLength(2);
    const [first, second] = posts(calls);
    expect(second.headers.get("Idempotency-Key")).toBe(first.headers.get("Idempotency-Key"));
    expect(second.body).toMatchObject({ subject: "Lunch today" });
  });

  it("reports that a lost send may have saved, with nothing edited and after a later refusal", async () => {
    serve([lost, () => problem(409, "/api/v0/problems/sending-account-needs-reconnect")]);
    const { loses } = await show(composeValues([studio.id], message));
    expect(loses.last).toBe(null);

    await userEvent.click(page.getByRole("button", { name: "Send" }));
    await expect.element(page.getByRole("alert")).toMatchTextContent("Sending again is safe");
    expect(loses.last).toBe("unconfirmed");

    await userEvent.click(page.getByRole("button", { name: "Send" }));
    await expect.element(page.getByRole("link", { name: "Open Connected accounts" })).toBeVisible();
    expect(loses.last).toBe("unconfirmed");
  });

  it("reports typed text, and a send running, as what closing would lose", async () => {
    // The send waits until the test answers it.
    vi.stubGlobal(
      "fetch",
      vi.fn<() => Promise<Response>>(() => new Promise(() => {})),
    );
    const { loses } = await show();

    await userEvent.type(page.getByRole("textbox", { name: "Subject" }), "Lunch");
    await expect.poll(() => loses.last).toBe("typed");
    await userEvent.click(page.getByRole("button", { name: /From/ }));
    await userEvent.click(page.getByRole("option", { name: "studio@example.com Studio" }));
    await userEvent.type(page.getByRole("textbox", { name: "To" }), "ana@example.com,");
    await userEvent.click(page.getByRole("button", { name: "Send" }));
    await expect.poll(() => loses.last).toBe("sending");
  });

  it("gives a new form a new key", async () => {
    const calls = serve();
    const first = await show(composeValues([studio.id], message));
    await userEvent.click(page.getByRole("button", { name: "Send" }));
    await expect.poll(() => posts(calls)).toHaveLength(1);
    await first.screen.unmount();

    await show(composeValues([studio.id], message));
    await userEvent.click(page.getByRole("button", { name: "Send" }));

    await expect.poll(() => posts(calls)).toHaveLength(2);
    const [one, two] = posts(calls);
    expect(two.headers.get("Idempotency-Key")).not.toBe(one.headers.get("Idempotency-Key"));
  });

  it("ignores an answer that comes after the member left, but rereads the Outbox", async () => {
    // The send waits until the test answers it.
    const answer: { resolve?: (response: Response) => void } = {};
    vi.stubGlobal(
      "fetch",
      vi.fn<() => Promise<Response>>(
        () =>
          new Promise((resolve) => {
            answer.resolve = resolve;
          }),
      ),
    );
    const { sent, client, router } = await show(composeValues([studio.id], message));
    const reread = vi.spyOn(client, "invalidateQueries");

    await userEvent.click(page.getByRole("button", { name: "Send" }));
    // The window closed while sending, after asking (compose-window's tests).
    void router.navigate({ href: "/elsewhere" });
    await expect.element(page.getByRole("heading", { name: "Elsewhere" })).toBeVisible();
    answer.resolve?.(Response.json({ data: message }, { status: created }));

    await expect
      .poll(() => reread.mock.calls.map(([filters]) => filters?.queryKey))
      .toContainEqual(getListOutgoingMessagesQueryKey());
    expect(sent).toEqual([]);
  });

  it("reports a 401 to the page", async () => {
    serve([() => problem(401, "/api/v0/problems/unauthenticated")]);
    const { unauthorized } = await show(composeValues([studio.id], message));

    await userEvent.click(page.getByRole("button", { name: "Send" }));

    await expect.poll(() => unauthorized.count).toBe(1);
  });

  it("rereads the accounts when the server says the account needs reconnecting", async () => {
    serve([() => problem(409, "/api/v0/problems/sending-account-needs-reconnect")]);
    const { client } = await show(composeValues([studio.id], message));
    const reread = vi.spyOn(client, "invalidateQueries");

    await userEvent.click(page.getByRole("button", { name: "Send" }));

    await expect.element(page.getByRole("link", { name: "Open Connected accounts" })).toBeVisible();
    expect(reread.mock.calls.map(([filters]) => filters?.queryKey)).toContainEqual(
      getListConnectedAccountsQueryKey(),
    );
  });
});
