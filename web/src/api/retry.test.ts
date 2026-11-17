import { CancelledError, MutationObserver, QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import {
  getCreateProbeEventMutationOptions,
  getGetHealthQueryOptions,
  getSendMessageAgainMutationOptions,
  getSendMessageMutationOptions,
} from "./generated/inbok";
import type { ProblemType } from "./generated/model";
import { repeatableWriteRetry, retryOptions } from "./retry";

const serviceBusyType: ProblemType = "/api/v0/problems/service-busy";
const clients: QueryClient[] = [];
const httpStatus = {
  ok: 200,
  badRequest: 400,
  unauthorized: 401,
  notFound: 404,
  requestTimeout: 408,
  conflict: 409,
  unprocessable: 422,
  tooManyRequests: 429,
  internalError: 500,
  badGateway: 502,
  unavailable: 503,
  gatewayTimeout: 504,
};

// Use the same defaults and generated operation as the app, with a controlled clock.
function client(): QueryClient {
  const queryClient = new QueryClient({ defaultOptions: retryOptions });
  clients.push(queryClient);
  return queryClient;
}

// Record actual send times while replacing only the network boundary.
function respond(status: number, headers: Record<string, string> = {}): number[] {
  const attempts: number[] = [];
  vi.stubGlobal("fetch", async () => {
    attempts.push(Date.now());
    return new Response("Temporary reply", { status, headers });
  });
  return attempts;
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-10-23T00:00:00Z"));
});

afterEach(() => {
  for (const queryClient of clients) queryClient.clear();
  clients.length = 0;
});

describe("read retries", () => {
  it("retries connection failures twice, after one and two seconds", async () => {
    const attempts: number[] = [];
    const fetch = vi.fn<typeof globalThis.fetch>(async () => {
      attempts.push(Date.now());
      throw new TypeError("Failed to fetch");
    });
    vi.stubGlobal("fetch", fetch);

    const result = client()
      .query(getGetHealthQueryOptions())
      .catch((err: unknown) => err);
    await vi.advanceTimersByTimeAsync(3_000);

    expect(attempts.map((time) => time - attempts[0])).toEqual([0, 1_000, 3_000]);
    await expect(result).resolves.toMatchObject({ kind: "network" });
  });

  it("honors Retry-After seconds on a 503 even when its body is plain text", async () => {
    const attempts: number[] = [];
    vi.stubGlobal("fetch", async () => {
      attempts.push(Date.now());
      return new Response("Service unavailable", {
        status: 503,
        headers: { "Retry-After": "3", "Content-Type": "text/plain" },
      });
    });

    const result = client()
      .query(getGetHealthQueryOptions())
      .catch((err: unknown) => err);
    await vi.advanceTimersByTimeAsync(6_000);

    expect(attempts.map((time) => time - attempts[0])).toEqual([0, 3_000, 6_000]);
    await expect(result).resolves.toMatchObject({ status: 503, retryAfter: "3" });
  });

  it.each([
    httpStatus.requestTimeout,
    httpStatus.tooManyRequests,
    httpStatus.badGateway,
    httpStatus.unavailable,
    httpStatus.gatewayTimeout,
  ])("retries actual HTTP %s twice", async (status) => {
    const attempts = respond(status);
    const result = client()
      .query(getGetHealthQueryOptions())
      .catch((err: unknown) => err);
    await vi.advanceTimersByTimeAsync(3_000);

    expect(attempts).toHaveLength(3);
    await expect(result).resolves.toMatchObject({ status });
  });

  it.each([
    httpStatus.badRequest,
    httpStatus.unauthorized,
    httpStatus.notFound,
    httpStatus.conflict,
    httpStatus.unprocessable,
    httpStatus.internalError,
  ])("shows actual HTTP %s after one attempt", async (status) => {
    const attempts = respond(status, { "Retry-After": "3" });
    const result = client()
      .query(getGetHealthQueryOptions())
      .catch((err: unknown) => err);
    await vi.advanceTimersByTimeAsync(10_000);

    expect(attempts).toHaveLength(1);
    await expect(result).resolves.toMatchObject({ status });
  });

  it("shows Go's service-busy 503 at once when its wait is longer than a retry may take", async () => {
    // Go sends this when a request runs out of its time, with a Retry-After
    // from its own timeout: here past the five seconds one retry may wait.
    const attempts: number[] = [];
    vi.stubGlobal("fetch", async () => {
      attempts.push(Date.now());
      return Response.json(
        {
          type: serviceBusyType,
          status: httpStatus.unavailable,
          title: "Service busy",
          detail: "The server is busy and could not finish the request in time.",
        },
        {
          status: httpStatus.unavailable,
          headers: { "Retry-After": "10", "Content-Type": "application/problem+json" },
        },
      );
    });

    const result = client()
      .query(getGetHealthQueryOptions())
      .catch((err: unknown) => err);
    await vi.advanceTimersByTimeAsync(30_000);

    expect(attempts).toHaveLength(1);
    await expect(result).resolves.toMatchObject({
      status: httpStatus.unavailable,
      retryAfter: "10",
      problem: { type: serviceBusyType },
    });
  });

  it("uses actual status rather than the problem body's advisory status", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>(
      async () =>
        new Response(JSON.stringify({ status: httpStatus.unprocessable }), {
          status: httpStatus.unavailable,
          headers: { "Content-Type": "application/problem+json" },
        }),
    );
    vi.stubGlobal("fetch", fetch);
    const result = client()
      .query(getGetHealthQueryOptions())
      .catch((err: unknown) => err);
    await vi.advanceTimersByTimeAsync(3_000);

    expect(fetch).toHaveBeenCalledTimes(3);
    await expect(result).resolves.toMatchObject({
      status: httpStatus.unavailable,
      problem: { status: httpStatus.unprocessable },
    });
  });

  it.each([
    { status: httpStatus.ok, attempts: 3 },
    { status: httpStatus.unprocessable, attempts: 1 },
    { status: httpStatus.unavailable, attempts: 3 },
  ])("keeps status $status authoritative when its body is interrupted", async (testCase) => {
    const fetch = vi.fn<typeof globalThis.fetch>(
      async () =>
        new Response(
          new ReadableStream({
            start(controller) {
              controller.error(new TypeError("Connection lost"));
            },
          }),
          { status: testCase.status },
        ),
    );
    vi.stubGlobal("fetch", fetch);
    const result = client()
      .query(getGetHealthQueryOptions())
      .catch((err: unknown) => err);
    await vi.advanceTimersByTimeAsync(3_000);

    expect(fetch).toHaveBeenCalledTimes(testCase.attempts);
    await expect(result).resolves.toMatchObject({ kind: "network", status: testCase.status });
  });

  it("does not retry a complete successful reply that breaks the contract", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const attempts = respond(httpStatus.ok);
    const result = client()
      .query(getGetHealthQueryOptions())
      .catch((err: unknown) => err);
    await vi.advanceTimersByTimeAsync(10_000);

    expect(attempts).toHaveLength(1);
    await expect(result).resolves.toMatchObject({ kind: "response", status: httpStatus.ok });
  });

  // retry-after.test.ts holds the header's rules: the forms of a date, and
  // which values are unusable. The tests here prove only that a retry's wait
  // comes from them.
  it("honors a Retry-After date on a 429", async () => {
    const attempts = respond(httpStatus.tooManyRequests, {
      "Retry-After": "Fri, 23 Oct 2026 00:00:03 GMT",
    });
    const result = client()
      .query(getGetHealthQueryOptions())
      .catch((err: unknown) => err);
    await vi.advanceTimersByTimeAsync(5_000);

    expect(attempts.map((time) => time - attempts[0])).toEqual([0, 3_000, 5_000]);
    await expect(result).resolves.toMatchObject({ status: httpStatus.tooManyRequests });
  });

  it.each([
    { header: "not a date", name: "an unusable Retry-After" },
    { header: "Fri, 23 Oct 2026 00:00:00 GMT", name: "a Retry-After date that has already passed" },
  ])("keeps normal backoff as the least wait for $name", async ({ header }) => {
    const attempts = respond(httpStatus.unavailable, { "Retry-After": header });
    const result = client()
      .query(getGetHealthQueryOptions())
      .catch((err: unknown) => err);
    await vi.advanceTimersByTimeAsync(3_000);

    expect(attempts.map((time) => time - attempts[0])).toEqual([0, 1_000, 3_000]);
    await expect(result).resolves.toMatchObject({ status: httpStatus.unavailable });
  });

  it("allows at most ten seconds of scheduled waiting across two retries", async () => {
    const attempts = respond(httpStatus.unavailable, { "Retry-After": "5" });
    const result = client()
      .query(getGetHealthQueryOptions())
      .catch((err: unknown) => err);
    await vi.advanceTimersByTimeAsync(10_000);

    expect(attempts.map((time) => time - attempts[0])).toEqual([0, 5_000, 10_000]);
    await expect(result).resolves.toMatchObject({ status: httpStatus.unavailable });
  });

  it.each(["6", "999999999999999999999999999999", "Fri, 23 Oct 2026 00:00:06 GMT"])(
    "stops rather than shortening an excessive Retry-After %s",
    async (header) => {
      const attempts = respond(httpStatus.unavailable, { "Retry-After": header });
      const result = client()
        .query(getGetHealthQueryOptions())
        .catch((err: unknown) => err);
      await vi.advanceTimersByTimeAsync(10_000);

      expect(attempts).toHaveLength(1);
      await expect(result).resolves.toMatchObject({ status: httpStatus.unavailable });
    },
  );

  it("ignores Retry-After outside 429 and 503", async () => {
    const attempts = respond(httpStatus.badGateway, { "Retry-After": "60" });
    const result = client()
      .query(getGetHealthQueryOptions())
      .catch((err: unknown) => err);
    await vi.advanceTimersByTimeAsync(3_000);

    expect(attempts.map((time) => time - attempts[0])).toEqual([0, 1_000, 3_000]);
    await expect(result).resolves.toMatchObject({ status: httpStatus.badGateway });
  });

  it("cancels pending retries when the read becomes obsolete", async () => {
    const attempts = respond(httpStatus.unavailable, { "Retry-After": "3" });
    const queryClient = client();
    const options = getGetHealthQueryOptions();
    const result = queryClient.query(options).catch((err: unknown) => err);
    await vi.advanceTimersByTimeAsync(0);
    await queryClient.cancelQueries({ queryKey: options.queryKey });
    await vi.advanceTimersByTimeAsync(10_000);

    expect(attempts).toHaveLength(1);
    expect(await result).toBeInstanceOf(CancelledError);
  });

  it("stops retrying after a successful attempt", async () => {
    const fetch = vi
      .fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(new Response("Unavailable", { status: httpStatus.unavailable }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: { status: "ok" } })));
    vi.stubGlobal("fetch", fetch);
    const result = client().query(getGetHealthQueryOptions());
    await vi.advanceTimersByTimeAsync(10_000);

    await expect(result).resolves.toEqual({ data: { status: "ok" } });
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("sends the probe write only once, even when its 503 includes Retry-After", async () => {
    const attempts = respond(httpStatus.unavailable, { "Retry-After": "3" });
    const mutation = new MutationObserver(client(), getCreateProbeEventMutationOptions());
    const result = mutation
      .mutate({ data: { note: "ok", amount: 1, highlighted: false } })
      .catch((err: unknown) => err);
    await vi.advanceTimersByTimeAsync(10_000);

    expect(attempts).toHaveLength(1);
    await expect(result).resolves.toMatchObject({ status: httpStatus.unavailable });
  });
});

describe("repeatable write retries", () => {
  it("sends Send Message again with the same key and body after a lost answer", async () => {
    const sent: { key: string | null; body: unknown }[] = [];
    const fetch = vi
      .fn<typeof globalThis.fetch>()
      .mockImplementationOnce(async (_, init) => {
        sent.push({ key: new Headers(init?.headers).get("Idempotency-Key"), body: init?.body });
        throw new TypeError("Failed to fetch");
      })
      .mockImplementation(async (_, init) => {
        sent.push({ key: new Headers(init?.headers).get("Idempotency-Key"), body: init?.body });
        return new Response("Unavailable", { status: httpStatus.unavailable });
      });
    vi.stubGlobal("fetch", fetch);
    const key = '"8e03978e-40d5-43e8-bc93-6894a57f9324"';
    const mutation = new MutationObserver(
      client(),
      getSendMessageMutationOptions({
        mutation: repeatableWriteRetry,
        request: { headers: { "Idempotency-Key": key } },
      }),
    );
    const data = {
      connectedAccountId: 1,
      to: ["ana@example.com"],
      cc: [],
      bcc: [],
      subject: "",
      body: "",
    };
    const result = mutation.mutate({ data }).catch((err: unknown) => err);
    await vi.advanceTimersByTimeAsync(10_000);

    await expect(result).resolves.toMatchObject({ status: httpStatus.unavailable });
    expect(fetch).toHaveBeenCalledTimes(3);
    expect(new Set(sent.map(({ key: sentKey }) => sentKey))).toEqual(new Set([key]));
    expect(new Set(sent.map(({ body }) => body))).toEqual(new Set([JSON.stringify(data)]));
  });

  it("sends Send Message Again again after a lost answer", async () => {
    const fetch = vi
      .fn<typeof globalThis.fetch>()
      .mockRejectedValueOnce(new TypeError("Failed to fetch"))
      .mockResolvedValue(new Response("Unavailable", { status: httpStatus.unavailable }));
    vi.stubGlobal("fetch", fetch);
    const mutation = new MutationObserver(
      client(),
      getSendMessageAgainMutationOptions({ mutation: repeatableWriteRetry }),
    );
    const result = mutation
      .mutate({ id: "0b9f3a52-5f1d-4a8e-9a43-5f0c2b6b1c11" })
      .catch((err: unknown) => err);
    await vi.advanceTimersByTimeAsync(10_000);

    await expect(result).resolves.toMatchObject({ status: httpStatus.unavailable });
    expect(fetch).toHaveBeenCalledTimes(3);
  });

  it("never repeats a refusal, e.g. a reused key", async () => {
    const attempts = respond(httpStatus.unprocessable);
    const mutation = new MutationObserver(
      client(),
      getSendMessageMutationOptions({ mutation: repeatableWriteRetry }),
    );
    const data = {
      connectedAccountId: 1,
      to: ["ana@example.com"],
      cc: [],
      bcc: [],
      subject: "",
      body: "",
    };
    const result = mutation.mutate({ data }).catch((err: unknown) => err);
    await vi.advanceTimersByTimeAsync(10_000);

    await expect(result).resolves.toMatchObject({ status: httpStatus.unprocessable });
    expect(attempts).toHaveLength(1);
  });
});
