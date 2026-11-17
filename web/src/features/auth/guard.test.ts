import { describe, expect, it } from "vite-plus/test";
import { ApiError } from "@/api/transport";
import { fakeClock } from "@/test/clock";
import { testQueryClient } from "@/test/query-client";
import { fakeServer, memberSession, problem } from "@/test/server";
import { readSession } from "./guard";

const busy = 503;
// Longer than any staleTime a session read could have by accident.
const anHour = 60 * 60 * 1000;

describe("readSession", () => {
  it("returns the signed-in member's session", async () => {
    fakeServer(memberSession).install();
    expect(await readSession(testQueryClient())).toEqual(memberSession);
  });

  it("returns undefined without a session", async () => {
    fakeServer().install();
    expect(await readSession(testQueryClient())).toBeUndefined();
  });

  it("throws any other failure for the route's error view", async () => {
    const server = fakeServer(memberSession).install();
    server.on("GET", "/api/v0/sessions/current", () =>
      problem(busy, "/api/v0/problems/service-busy", { retryAfter: 1 }),
    );
    await expect(readSession(testQueryClient())).rejects.toBeInstanceOf(ApiError);
  });

  it("reads the session once, then uses the cached one", async () => {
    const server = fakeServer(memberSession).install();
    const queries = testQueryClient();
    await readSession(queries);
    await readSession(queries);
    expect(server.requests).toHaveLength(1);
  });

  it("uses a cached session however old, since pages reread it themselves", async () => {
    const clock = fakeClock();
    const server = fakeServer(memberSession).install();
    const queries = testQueryClient();
    await readSession(queries);
    await clock.advance(anHour);
    await readSession(queries);
    expect(server.requests).toHaveLength(1);
  });
});
