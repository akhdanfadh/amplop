import { CancelledError, MutationObserver, QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { getCreateProbeEventMutationOptions, getGetHealthQueryOptions } from "./generated/inbok";
import { retryOptions } from "./retry";

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
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  vi.useRealTimers();
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

  it.each([
    "Fri, 23 Oct 2026 00:00:03 GMT",
    "Friday, 23-Oct-26 00:00:03 GMT",
    "Fri Oct 23 00:00:03 2026",
  ])("honors Retry-After date %s on a 429", async (header) => {
    const attempts = respond(httpStatus.tooManyRequests, { "Retry-After": header });
    const result = client()
      .query(getGetHealthQueryOptions())
      .catch((err: unknown) => err);
    await vi.advanceTimersByTimeAsync(5_000);

    expect(attempts.map((time) => time - attempts[0])).toEqual([0, 3_000, 5_000]);
    await expect(result).resolves.toMatchObject({ status: httpStatus.tooManyRequests });
  });

  it.each(["", "not a date", "-1", "1.5", "0x10", "Fri, 99 Oct 2026 00:00:03 GMT"])(
    "uses normal backoff for unusable Retry-After %s",
    async (header) => {
      const attempts = respond(httpStatus.unavailable, { "Retry-After": header });
      const result = client()
        .query(getGetHealthQueryOptions())
        .catch((err: unknown) => err);
      await vi.advanceTimersByTimeAsync(3_000);

      expect(attempts.map((time) => time - attempts[0])).toEqual([0, 1_000, 3_000]);
      await expect(result).resolves.toMatchObject({ status: httpStatus.unavailable });
    },
  );

  it("keeps normal backoff when the server's date has already passed", async () => {
    const attempts = respond(httpStatus.unavailable, {
      "Retry-After": "Fri, 23 Oct 2026 00:00:00 GMT",
    });
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

  it("applies HTTP's fifty-year rule to a legacy date's two-digit year", async () => {
    const attempts = respond(httpStatus.unavailable, {
      "Retry-After": "Thursday, 23-Oct-76 00:00:00 GMT",
    });
    const result = client()
      .query(getGetHealthQueryOptions())
      .catch((err: unknown) => err);
    await vi.advanceTimersByTimeAsync(10_000);

    expect(attempts).toHaveLength(1);
    await expect(result).resolves.toMatchObject({ status: httpStatus.unavailable });
  });

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
