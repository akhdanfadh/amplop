import { describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import {
  getListConnectedAccountsQueryKey,
  getListOutgoingMessagesQueryKey,
} from "@/api/generated/inbok";
import type { ListedAccount, OutgoingMessage } from "@/api/generated/model";
import { repeatableWriteRetry } from "@/api/retry";
import { ApiError } from "@/api/transport";
import { fakeClock } from "@/test/clock";
import { listedAccount, outgoingMessage } from "@/test/mail-api";
import {
  type Asked,
  type Server,
  data,
  fakeServer,
  held,
  memberSession,
  problem,
} from "@/test/server";
import { stubRouter } from "@/test/stub-router";
import { ComposeForm } from "./compose-form";
import { composeValues, type ComposeValues } from "./compose-schema";
import type { LeaveState } from "./text";

const created = 201;
const badGateway = 502;

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
const message = outgoingMessage({ id: "019a0000-0000-7000-8000-0000000000bb" });

// accepted is Send Message's answer once the server saved the message.
function accepted(): Response {
  return data(message, created);
}

// serve installs a signed-in member's server whose Send Message answers
// with each of answers in turn, the last one repeating, and returns it.
function serve(answers: (() => Response | Promise<Response>)[] = [accepted]): Server {
  const server = fakeServer(memberSession).install();
  let sends = 0;
  server.on("POST", "/api/v0/outgoing-messages", () => {
    const answer = answers[Math.min(sends, answers.length - 1)];
    sends++;
    return answer();
  });
  return server;
}

// show renders the compose form starting with initial, sending from one of
// accounts, as the docked window draws it, inside a router with a second
// page, /elsewhere, to leave for. It returns the messages the form
// reported sent, what closing would lose as it last reported, the query
// client, the router, and the rendered screen.
async function show(
  initial: ComposeValues = composeValues(),
  accounts: ListedAccount[] = [studio, stale],
) {
  const sent: OutgoingMessage[] = [];
  const loses: { last: LeaveState | null } = { last: null };
  const { client, router, screen } = await stubRouter(
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
    />,
    { pages: ["/elsewhere"] },
  );
  return { sent, loses, client, router, screen };
}

// write chooses Studio in From and fills To, Subject, and Message.
async function write(): Promise<void> {
  await userEvent.click(page.getByRole("button", { name: /From/ }));
  await userEvent.click(page.getByRole("option", { name: "studio@example.com Studio" }));
  await userEvent.type(page.getByRole("textbox", { name: "To" }), "ana@example.com,");
  await userEvent.type(page.getByRole("textbox", { name: "Subject" }), "Lunch");
  await userEvent.type(page.getByRole("textbox", { name: "Message" }), "Noon?");
}

// lost is an answer that says nothing of whether the message was saved.
function lost(): Response {
  return problem(500, "/api/v0/problems/internal-error");
}

// posts returns the Send Message requests server got.
function posts(server: Server): Asked[] {
  return server.requests.filter((asked) => asked.method === "POST");
}

// keyReused is Send Message's refusal of a key that saved another message.
function keyReused(): Response {
  return problem(422, "/api/v0/problems/idempotency-key-reused");
}

// waiting returns a Send Message answer that saves once hold resolves.
function waiting(hold: { promise: Promise<void> }): () => Promise<Response> {
  return async () => {
    await hold.promise;
    return accepted();
  };
}

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
    await show(composeValues({ ...message, cc: ["ben@example.com"] }));

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
    await show(composeValues(), [{ ...studio, allowedActions: ["rename", "disconnect"] }]);

    await userEvent.click(page.getByRole("button", { name: /From/ }));
    await expect
      .element(page.getByRole("option", { name: "studio@example.com Studio" }))
      .toHaveAttribute("aria-disabled", "true");
  });

  it("shows errors only after Send, with focus on the first", async () => {
    const server = serve();
    await show();

    await expect
      .element(page.getByText("Choose the account to send from."))
      .not.toBeInTheDocument();
    await userEvent.click(page.getByRole("button", { name: "Send" }));

    await expect.element(page.getByText("Choose the account to send from.")).toBeVisible();
    await expect.element(page.getByText("Add at least one recipient.")).toBeVisible();
    await expect.element(page.getByRole("button", { name: /From/ })).toHaveFocus();
    expect(posts(server)).toHaveLength(0);
  });

  it("sends with Ctrl+Enter from any field, with a key, and reports the saved message", async () => {
    const server = serve();
    const { sent } = await show();

    await write();
    await userEvent.click(page.getByRole("textbox", { name: "Subject" }));
    await userEvent.keyboard("{Control>}{Enter}{/Control}");

    await expect.poll(() => sent).toEqual([message]);
    const [post] = posts(server);
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
    serve([waiting(held())]);
    await show();
    await write();

    await userEvent.click(page.getByRole("button", { name: "Send" }));

    await expect.element(page.getByRole("button", { name: /Sending/ })).toHaveFocus();
    await expect.element(page.getByRole("textbox", { name: "Message" })).toBeDisabled();
    await expect.element(page.getByRole("textbox", { name: "To" })).toHaveAttribute("readonly");
  });

  it("sends a new message with a new key after the key saved another", async () => {
    const server = serve([keyReused, accepted]);
    const { sent } = await show();
    await write();

    await userEvent.click(page.getByRole("button", { name: "Send" }));
    await expect.element(page.getByRole("alert")).toMatchTextContent("Check the Outbox");
    await expect
      .element(page.getByRole("link", { name: "Open the Outbox in a new tab" }))
      .toBeVisible();

    await userEvent.click(page.getByRole("button", { name: "Send" }));
    await expect.poll(() => sent).toEqual([message]);
    const keys = posts(server).map((asked) => asked.headers.get("Idempotency-Key"));
    expect(keys).toHaveLength(2);
    expect(keys[0]).not.toBe(keys[1]);
  });

  it("repeats a lost send with the same key and content, by itself and by hand", async () => {
    const server = serve([
      // A proxy's answer, which has none of the contract's types.
      () => problem(badGateway),
      lost,
      accepted,
    ]);
    const clock = fakeClock();
    const { sent, client } = await show();
    await write();

    await userEvent.click(page.getByRole("button", { name: "Send" }));
    // The 502 is repeated by itself after the first repeat's wait; the 500
    // is not. The wait runs once the send counts its failure.
    await expect.poll(() => client.getMutationCache().getAll().at(-1)?.state.failureCount).toBe(1);
    expect(posts(server)).toHaveLength(1);
    await clock.advance(
      repeatableWriteRetry.retryDelay(0, new ApiError("http", { status: badGateway })),
    );
    await expect.poll(() => posts(server)).toHaveLength(2);
    await expect.element(page.getByRole("alert")).toMatchTextContent("Sending again is safe");
    await userEvent.click(page.getByRole("button", { name: "Send" }));

    await expect.poll(() => sent).toEqual([message]);
    const sends = posts(server);
    expect(sends).toHaveLength(3);
    expect(new Set(sends.map((asked) => asked.headers.get("Idempotency-Key"))).size).toBe(1);
    expect(sends[2].body).toEqual(sends[0].body);
  });

  it("keeps the key through edits after a lost send, so the server decides", async () => {
    const server = serve([lost, accepted]);
    await show();
    await write();

    await userEvent.click(page.getByRole("button", { name: "Send" }));
    await expect.element(page.getByRole("alert")).toMatchTextContent("Sending again is safe");
    await userEvent.type(page.getByRole("textbox", { name: "Subject" }), " today");
    await userEvent.click(page.getByRole("button", { name: "Send" }));

    await expect.poll(() => posts(server)).toHaveLength(2);
    const [first, second] = posts(server);
    expect(second.headers.get("Idempotency-Key")).toBe(first.headers.get("Idempotency-Key"));
    expect(second.body).toMatchObject({ subject: "Lunch today" });
  });

  it("reports that a lost send may have saved, with nothing edited and after a later refusal", async () => {
    serve([lost, () => problem(409, "/api/v0/problems/sending-account-needs-reconnect")]);
    const { loses } = await show(composeValues(message));
    expect(loses.last).toBe(null);

    await userEvent.click(page.getByRole("button", { name: "Send" }));
    await expect.element(page.getByRole("alert")).toMatchTextContent("Sending again is safe");
    expect(loses.last).toBe("unconfirmed");

    await userEvent.click(page.getByRole("button", { name: "Send" }));
    await expect.element(page.getByRole("link", { name: "Open Connected accounts" })).toBeVisible();
    expect(loses.last).toBe("unconfirmed");
  });

  it("reports typed text, and a send running, as what closing would lose", async () => {
    // The send waits for an answer that never comes.
    serve([waiting(held())]);
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
    const server = serve();
    const first = await show(composeValues(message));
    await userEvent.click(page.getByRole("button", { name: "Send" }));
    await expect.poll(() => posts(server)).toHaveLength(1);
    await first.screen.unmount();

    await show(composeValues(message));
    await userEvent.click(page.getByRole("button", { name: "Send" }));

    await expect.poll(() => posts(server)).toHaveLength(2);
    const [one, two] = posts(server);
    expect(two.headers.get("Idempotency-Key")).not.toBe(one.headers.get("Idempotency-Key"));
  });

  it("ignores an answer that comes after the member left, but rereads the Outbox", async () => {
    // The send waits until the test answers it.
    const answer = held();
    serve([waiting(answer)]);
    const { sent, client, router } = await show(composeValues(message));
    const reread = vi.spyOn(client, "invalidateQueries");

    await userEvent.click(page.getByRole("button", { name: "Send" }));
    // The window closed while sending, after asking (compose-window's tests).
    void router.navigate({ href: "/elsewhere" });
    await expect.element(page.getByRole("heading", { name: "/elsewhere" })).toBeVisible();
    answer.resolve();

    await expect
      .poll(() => reread.mock.calls.map(([filters]) => filters?.queryKey))
      .toContainEqual(getListOutgoingMessagesQueryKey());
    expect(sent).toEqual([]);
  });

  it("rereads the accounts when the server says the account needs reconnecting", async () => {
    serve([() => problem(409, "/api/v0/problems/sending-account-needs-reconnect")]);
    const { client } = await show(composeValues(message));
    const reread = vi.spyOn(client, "invalidateQueries");

    await userEvent.click(page.getByRole("button", { name: "Send" }));

    await expect.element(page.getByRole("link", { name: "Open Connected accounts" })).toBeVisible();
    expect(reread.mock.calls.map(([filters]) => filters?.queryKey)).toContainEqual(
      getListConnectedAccountsQueryKey(),
    );
  });
});
