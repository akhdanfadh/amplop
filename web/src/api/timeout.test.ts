import { MutationObserver, QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import {
  getRequestPasswordResetLinkMutationOptions,
  getGetHealthQueryOptions,
  getHealth,
} from "./generated/inbok";
import { userMessage } from "./problem";
import { retryOptions } from "./retry";

const attemptTimeoutMs = 15_000;
const headersDelayMs = 5_000;
const httpStatus = { ok: 200, unprocessable: 422, unavailable: 503 };
const clients: QueryClient[] = [];

// A stalled network operation rejects only when its supplied signal aborts.
function untilAborted<T>(signal: AbortSignal | null | undefined): Promise<T> {
  return new Promise((_resolve, reject) => {
    if (!signal) return;
    if (signal.aborted) {
      reject(signal.reason);
      return;
    }
    signal.addEventListener("abort", () => reject(signal.reason), { once: true });
  });
}

// Headers arrive after five seconds; the body never finishes on its own.
function stalledBody(status: number): void {
  vi.stubGlobal(
    "fetch",
    (_url: RequestInfo | URL, init?: RequestInit) =>
      new Promise<Response>((resolve) => {
        setTimeout(() => {
          resolve(
            new Response(
              new ReadableStream({
                start(controller) {
                  init?.signal?.addEventListener(
                    "abort",
                    () => controller.error(init.signal?.reason),
                    { once: true },
                  );
                },
              }),
              {
                status,
                headers: { "X-Request-ID": "slow-response", "Retry-After": "3" },
              },
            ),
          );
        }, headersDelayMs);
      }),
  );
}

// Exercise the generated operations with the same Query defaults as the app.
function client(): QueryClient {
  const queryClient = new QueryClient({ defaultOptions: retryOptions });
  clients.push(queryClient);
  return queryClient;
}

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  for (const queryClient of clients) queryClient.clear();
  clients.length = 0;
});

describe("request timeouts", () => {
  it("fails a stalled send after fifteen seconds", async () => {
    vi.stubGlobal("fetch", (_url: RequestInfo | URL, init?: RequestInit) =>
      untilAborted<Response>(init?.signal),
    );
    const settled = vi.fn<(value: unknown) => void>();
    const result = getHealth().catch((err: unknown) => err);
    void result.then(settled);

    await vi.advanceTimersByTimeAsync(attemptTimeoutMs - 1);
    expect(settled).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);

    expect(settled).toHaveBeenCalledOnce();
    await expect(result).resolves.toMatchObject({ kind: "timeout", method: "GET" });
    expect(vi.getTimerCount()).toBe(0);
  });

  it.each([httpStatus.ok, httpStatus.unprocessable, httpStatus.unavailable])(
    "keeps HTTP %s facts when a stalled body uses up the same attempt budget",
    async (status) => {
      stalledBody(status);
      const settled = vi.fn<(value: unknown) => void>();
      const result = getHealth().catch((err: unknown) => err);
      void result.then(settled);

      await vi.advanceTimersByTimeAsync(attemptTimeoutMs - 1);
      expect(settled).not.toHaveBeenCalled();
      await vi.advanceTimersByTimeAsync(1);

      expect(settled).toHaveBeenCalledOnce();
      await expect(result).resolves.toMatchObject({
        kind: "timeout",
        status,
        requestId: "slow-response",
        retryAfter: "3",
      });
      expect(vi.getTimerCount()).toBe(0);
    },
  );

  it("preserves deliberate cancellation during body reading", async () => {
    stalledBody(httpStatus.ok);
    const caller = new AbortController();
    const cancelled = new DOMException("Obsolete read", "AbortError");
    const result = getHealth({ signal: caller.signal }).catch((err: unknown) => err);
    await vi.advanceTimersByTimeAsync(headersDelayMs);

    caller.abort(cancelled);

    await expect(result).resolves.toBe(cancelled);
    expect(vi.getTimerCount()).toBe(0);
  });

  it.each([httpStatus.ok, httpStatus.unavailable])(
    "releases the timer after a complete HTTP %s reply",
    async (status) => {
      vi.stubGlobal(
        "fetch",
        async () => new Response(JSON.stringify({ data: { status: "ok" } }), { status }),
      );

      await getHealth().catch((err: unknown) => err);

      expect(vi.getTimerCount()).toBe(0);
    },
  );

  it("does not repeat a read that exhausted its waiting limit", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>((_url, init) =>
      untilAborted<Response>(init?.signal),
    );
    vi.stubGlobal("fetch", fetch);
    const queryClient = client();
    const options = getGetHealthQueryOptions();
    const result = queryClient.query(options).catch((err: unknown) => err);

    await vi.advanceTimersByTimeAsync(attemptTimeoutMs);

    expect(queryClient.getQueryState(options.queryKey)?.status).toBe("error");
    await expect(result).resolves.toMatchObject({ kind: "timeout" });
    await vi.advanceTimersByTimeAsync(60_000);
    expect(fetch).toHaveBeenCalledOnce();
  });

  it.each([httpStatus.ok, httpStatus.unprocessable, httpStatus.unavailable])(
    "stops after a body-read timeout even with HTTP %s headers",
    async (status) => {
      stalledBody(status);
      const queryClient = client();
      const options = getGetHealthQueryOptions();
      const result = queryClient.query(options).catch((err: unknown) => err);

      await vi.advanceTimersByTimeAsync(attemptTimeoutMs);

      expect(queryClient.getQueryState(options.queryKey)?.status).toBe("error");
      await expect(result).resolves.toMatchObject({ kind: "timeout", status });
      await vi.advanceTimersByTimeAsync(60_000);
      expect(queryClient.getQueryState(options.queryKey)?.fetchFailureCount).toBe(1);
    },
  );

  it("gives a connection-failure retry its own waiting limit", async () => {
    const attempts: number[] = [];
    vi.stubGlobal("fetch", (_url: RequestInfo | URL, init?: RequestInit) => {
      attempts.push(Date.now());
      if (attempts.length === 1) return Promise.reject(new TypeError("Connection lost"));
      return untilAborted<Response>(init?.signal);
    });
    const queryClient = client();
    const options = getGetHealthQueryOptions();
    const result = queryClient.query(options).catch((err: unknown) => err);

    await vi.advanceTimersByTimeAsync(attemptTimeoutMs);
    expect(queryClient.getQueryState(options.queryKey)?.status).toBe("pending");
    await vi.advanceTimersByTimeAsync(1_000);

    expect(attempts.map((time) => time - attempts[0])).toEqual([0, 1_000]);
    expect(queryClient.getQueryState(options.queryKey)?.status).toBe("error");
    await expect(result).resolves.toMatchObject({ kind: "timeout" });
    await vi.advanceTimersByTimeAsync(60_000);
    expect(attempts).toHaveLength(2);
  });

  it("ends a timed-out write without automatic resubmission", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>((_url, init) =>
      untilAborted<Response>(init?.signal),
    );
    vi.stubGlobal("fetch", fetch);
    const mutation = new MutationObserver(client(), getRequestPasswordResetLinkMutationOptions());
    const result = mutation
      .mutate({ data: { email: "ana@example.com" } })
      .catch((err: unknown) => err);

    await vi.advanceTimersByTimeAsync(attemptTimeoutMs);

    expect(mutation.getCurrentResult()).toMatchObject({ status: "error", isPending: false });
    const error = mutation.getCurrentResult().error;
    if (!error) throw new Error("Expected a failed write");
    expect(userMessage(error)).toBe(
      "We couldn't confirm whether this was saved. Check before submitting again.",
    );
    await expect(result).resolves.toMatchObject({ kind: "timeout", method: "POST" });

    await vi.advanceTimersByTimeAsync(60_000);
    expect(fetch).toHaveBeenCalledOnce();
  });
});
