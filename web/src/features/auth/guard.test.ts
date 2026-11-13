import { QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { ApiError } from "@/api/transport";
import { readSession } from "./guard";

const session = {
  email: "ana@example.com",
  emailVerified: true,
  role: "member",
  totpRequired: false,
};
const problem = "application/problem+json";
const ok = 200;
const unauthorized = 401;
const busy = 503;
// Longer than any staleTime a session read could have by accident.
const anHour = 60 * 60 * 1000;

// Makes every fetch answer with body and status, and counts the calls.
function answer(body: unknown, status: number, type = "application/json") {
  const fetch = vi.fn<() => Promise<Response>>(
    async () => new Response(JSON.stringify(body), { status, headers: { "Content-Type": type } }),
  );
  vi.stubGlobal("fetch", fetch);
  return fetch;
}

// A client without retries, so a failure ends the read at once.
function client(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("readSession", () => {
  it("returns the signed-in member's session", async () => {
    answer({ data: session }, ok);
    expect(await readSession(client())).toEqual(session);
  });

  it("returns undefined without a session", async () => {
    answer(
      {
        type: "/api/v0/problems/unauthorized",
        status: unauthorized,
        title: "Unauthorized",
        detail: "Sign in.",
      },
      unauthorized,
      problem,
    );
    expect(await readSession(client())).toBeUndefined();
  });

  it("throws any other failure for the route's error view", async () => {
    answer(
      { type: "/api/v0/problems/service-busy", status: busy, title: "Busy", detail: "Try later." },
      busy,
      problem,
    );
    await expect(readSession(client())).rejects.toBeInstanceOf(ApiError);
  });

  it("reads the session once, then uses the cached one", async () => {
    const fetch = answer({ data: session }, ok);
    const queries = client();
    await readSession(queries);
    await readSession(queries);
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("uses a cached session however old, since pages reread it themselves", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    const fetch = answer({ data: session }, ok);
    const queries = client();
    await readSession(queries);
    vi.setSystemTime(Date.now() + anHour);
    await readSession(queries);
    expect(fetch).toHaveBeenCalledTimes(1);
  });
});
