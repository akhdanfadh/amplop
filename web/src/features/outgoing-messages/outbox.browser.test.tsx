import type { ReactNode } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it } from "vite-plus/test";
import { renderHook } from "vitest-browser-react";
import { getListOutgoingMessagesQueryKey } from "@/api/generated/inbok";
import type { OutgoingMessage, OutgoingMessageSummary } from "@/api/generated/model";
import { outgoingMessage } from "@/test/mail-api";
import { testQueryClient } from "@/test/query-client";
import { data, fakeServer, memberSession, problem } from "@/test/server";
import { useOutgoingMessage } from "./outbox";
import { sendPollMs } from "./poll";

const internalError = 500;
const serviceUnavailable = 503;

// A message saved and waiting to be sent.
const waiting = outgoingMessage();
const sending: OutgoingMessage = { ...waiting, status: "sending" };
const sent: OutgoingMessage = { ...waiting, status: "sent", sentAt: "2026-11-16T08:00:05Z" };
// Another message, e.g. the copy a Send again saved.
const copy: OutgoingMessage = { ...sent, id: "019a0000-0000-7000-8000-0000000000bb" };

// The reads serve counts.
type Reads = { outbox: number; message: number };

// summary returns message as the Outbox lists it, without its body.
function summary({ body: _body, ...rest }: OutgoingMessage): OutgoingMessageSummary {
  return rest;
}

// serve installs a fake server: each read of the Outbox lists the next of outbox,
// the last one repeating, and each read of the message answers with the
// next of message, the first after firstMessageMs; an entry that is a
// number answers with that status. It returns the reads made so far.
function serve({
  outbox,
  message,
  firstMessageMs = 0,
}: {
  outbox: (OutgoingMessage[] | number)[];
  message: (OutgoingMessage | number)[];
  firstMessageMs?: number;
}): Reads {
  const reads: Reads = { outbox: 0, message: 0 };
  const server = fakeServer(memberSession).install();
  server.on("GET", "/api/v0/outgoing-messages", () => {
    const answer = outbox[Math.min(reads.outbox++, outbox.length - 1)];
    return typeof answer === "number" ? busy(answer) : data({ messages: answer.map(summary) });
  });
  server.on("GET", "/api/v0/outgoing-messages/{id}", async () => {
    const answer = message[Math.min(reads.message++, message.length - 1)];
    if (reads.message === 1) await new Promise((resolve) => setTimeout(resolve, firstMessageMs));
    return typeof answer === "number" ? busy(answer) : data(answer);
  });
  return reads;
}

// busy answers a read with status, as a busy server does.
function busy(status: number): Response {
  return problem(status, "/api/v0/problems/service-busy", { title: "", detail: "" });
}

// read renders useOutgoingMessage for the message. It returns the hook's
// result and the query client.
async function read() {
  const client = testQueryClient();
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const { result } = await renderHook(() => useOutgoingMessage(waiting.id), { wrapper });
  return { result, client };
}

// pastPoll waits longer than one poll, so a read still polling shows in
// the counts.
async function pastPoll(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, sendPollMs + 500));
}

describe("useOutgoingMessage", () => {
  it("takes a listed message's status from the Outbox, its content from its read", async () => {
    // The message's own read answered with an older status.
    serve({ outbox: [[sent]], message: [sending] });
    const { result } = await read();

    await expect.poll(() => result.current.message?.status).toBe("sent");
    expect(result.current.message?.body).toBe("Noon?");
  });

  it("reads a listed message once while the Outbox follows its send", async () => {
    const reads = serve({ outbox: [[waiting], [sending], [sent]], message: [waiting] });
    const { result } = await read();

    await expect.poll(() => result.current.message?.status, { timeout: 6_000 }).toBe("sent");
    // Nothing is read again once the send ended.
    await pastPoll();
    expect(reads.outbox).toBe(3);
    expect(reads.message).toBe(1);
  });

  it("follows a message the Outbox doesn't hold by its own read", async () => {
    // E.g. one outside the newest 50, or saved after the Outbox was read.
    const reads = serve({ outbox: [[]], message: [sending, sent] });
    const { result } = await read();

    await expect.poll(() => result.current.message?.status, { timeout: 4_000 }).toBe("sent");
    await pastPoll();
    expect(reads.message).toBe(2);
    expect(reads.outbox).toBe(1);
  });

  it("hands a message from its own read to the Outbox once it lists it", async () => {
    // Saved after the Outbox was read; Send then reads the Outbox again.
    const reads = serve({ outbox: [[], [sending], [sent]], message: [waiting] });
    const { result, client } = await read();

    await expect.poll(() => result.current.message?.status).toBe("waiting");
    await expect
      .poll(() => client.getQueryState(getListOutgoingMessagesQueryKey())?.status)
      .toBe("success");
    await client.invalidateQueries({ queryKey: getListOutgoingMessagesQueryKey() });
    await expect.poll(() => result.current.message?.status, { timeout: 4_000 }).toBe("sent");
    await pastPoll();
    // The Outbox followed it from there; its own read stopped.
    expect(reads.message).toBe(1);
    expect(reads.outbox).toBe(3);
  });

  it("reads a message again once it leaves the Outbox", async () => {
    // Sent again elsewhere; its copy then pushes it past the newest 50.
    const failed: OutgoingMessage = { ...waiting, status: "failed", failure: "gave-up" };
    const tried: OutgoingMessage = { ...failed, sentAgainAs: copy.id };
    const reads = serve({
      outbox: [[{ ...copy, status: "sending" }, tried], [copy]],
      message: [failed, tried],
    });
    const { result } = await read();

    await expect.poll(() => result.current.message?.sentAgainAs).toBe(copy.id);
    await expect.poll(() => reads.outbox, { timeout: 4_000 }).toBe(2);
    await expect.poll(() => reads.message).toBe(2);
    expect(result.current.message?.sentAgainAs).toBe(copy.id);
  });

  it("reads a message leaving the Outbox afresh while its first read runs", async () => {
    const failed: OutgoingMessage = { ...waiting, status: "failed", failure: "gave-up" };
    const tried: OutgoingMessage = { ...failed, sentAgainAs: copy.id };
    // The first read answers after the message has left, with what it held
    // before the list's last status.
    const reads = serve({
      outbox: [[{ ...copy, status: "sending" }, tried], [copy]],
      message: [failed, tried],
      firstMessageMs: sendPollMs + 1_000,
    });
    const { result } = await read();

    await expect.poll(() => reads.outbox, { timeout: 4_000 }).toBe(2);
    await expect.poll(() => result.current.message?.sentAgainAs).toBe(copy.id);
    // The first read's older answer, arriving after, changes nothing.
    await pastPoll();
    expect(reads.message).toBe(2);
    expect(result.current.message?.sentAgainAs).toBe(copy.id);
  });

  it("gives the Outbox's failure, and reads only it again", async () => {
    const reads = serve({ outbox: [serviceUnavailable, [sent]], message: [sending] });
    const { result } = await read();

    await expect.poll(() => result.current.error?.status).toBe(serviceUnavailable);
    // Its content shows with the status its own read gave.
    expect(result.current.message?.status).toBe("sending");
    result.current.retry();
    await expect.poll(() => result.current.message?.status).toBe("sent");
    expect(result.current.error).toBeNull();
    expect(reads.message).toBe(1);
  });

  it("keeps the status the Outbox last listed when its reread fails", async () => {
    const reads = serve({ outbox: [[sending], serviceUnavailable, [sent]], message: [waiting] });
    const { result } = await read();

    await expect
      .poll(() => result.current.error?.status, { timeout: 4_000 })
      .toBe(serviceUnavailable);
    expect(result.current.message?.status).toBe("sending");
    // A failed read stops its polling until Try again.
    await pastPoll();
    expect(reads.outbox).toBe(2);
    result.current.retry();
    await expect.poll(() => result.current.message?.status).toBe("sent");
    expect(result.current.error).toBeNull();
    expect(reads.message).toBe(1);
  });

  it("gives the message's own failure first, and reads both again", async () => {
    const reads = serve({
      outbox: [serviceUnavailable, [sent]],
      message: [internalError, sending],
    });
    const { result } = await read();

    await expect.poll(() => result.current.error?.status).toBe(internalError);
    expect(result.current.message).toBeUndefined();
    result.current.retry();
    await expect.poll(() => result.current.message?.status).toBe("sent");
    expect(result.current.error).toBeNull();
    expect(reads).toEqual({ outbox: 2, message: 2 });
  });
});
