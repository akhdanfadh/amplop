import { describe, expect, it } from "vite-plus/test";
import {
  adminSession,
  data,
  expectAllAnswered,
  fakeServer,
  lose,
  memberSession,
  problem,
  testedSystemEmail,
} from "./server";

const origin = "http://inbok.test";

describe("fakeServer", () => {
  it("answers a route's path with its parameters, the request kept in the log", async () => {
    const server = fakeServer(memberSession);
    server.on("PATCH", "/api/v0/connected-accounts/{id}", ({ params, body }) =>
      data({ id: params.id, body }),
    );

    const answer = await server.fetch(`${origin}/api/v0/connected-accounts/7?x=1`, {
      method: "PATCH",
      body: JSON.stringify({ name: "Work" }),
      headers: { "Idempotency-Key": "k" },
    });

    expect(await answer.json()).toEqual({ data: { id: "7", body: { name: "Work" } } });
    expect(server.requests).toHaveLength(1);
    expect(server.requests[0]).toMatchObject({
      method: "PATCH",
      path: "/api/v0/connected-accounts/7?x=1",
      body: { name: "Work" },
    });
    expect(server.requests[0].headers.get("Idempotency-Key")).toBe("k");
  });

  it("prefers the route with fewer parameters, then the later one", async () => {
    const server = fakeServer(memberSession);
    server.on("GET", "/api/v0/conversations/search", () => data("search"));
    server.on("GET", "/api/v0/conversations/{id}", () => data("first"));
    server.on("GET", "/api/v0/conversations/{id}", () => data("second"));

    const search = await server.fetch(`${origin}/api/v0/conversations/search`);
    const one = await server.fetch(`${origin}/api/v0/conversations/abc`);

    expect(await search.json()).toEqual({ data: "search" });
    expect(await one.json()).toEqual({ data: "second" });
  });

  it("answers once with a one-time route, which can pass to the earlier one", async () => {
    const server = fakeServer(memberSession);
    server.on("GET", "/api/v0/places", () => data("usual"));
    server.once("GET", "/api/v0/places", () => problem(503, "/api/v0/problems/service-busy"));
    server.once("GET", "/api/v0/places", (_, earlier) => earlier());

    const statuses = [];
    for (let i = 0; i < 3; i++) {
      // One request after another, as the order is what is tested.
      // oxlint-disable-next-line no-await-in-loop
      statuses.push((await server.fetch(`${origin}/api/v0/places`)).status);
    }

    // The passing route's turn went to the one before it, used up too.
    expect(statuses).toEqual([503, 200, 200]);
  });

  it("answers a request no route knows with a 404, and keeps it", async () => {
    const server = fakeServer(memberSession);

    const answer = await server.fetch(`${origin}/api/v0/places`);

    expect(answer.status).toBe(404);
    expect(answer.headers.get("Content-Type")).toBe("application/problem+json");
    expect(server.unknown.map((asked) => `${asked.method} ${asked.path}`)).toEqual([
      "GET /api/v0/places",
    ]);
  });

  it("answers as a quiet inbok until a test's own route replaces an answer", async () => {
    const server = fakeServer(adminSession);
    const read = async (path: string) => (await server.fetch(`${origin}${path}`)).json();

    expect(await read("/api/v0/setup")).toEqual({ data: { status: "done" } });
    expect(await read("/api/v0/sessions/current")).toEqual({ data: adminSession });
    expect(await read("/api/v0/connected-accounts")).toEqual({
      data: { accounts: [], syncStopped: false },
    });
    expect(await read("/api/v0/system-email")).toEqual({ data: { settings: testedSystemEmail } });
    expect(await read("/api/v0/registration")).toEqual({ data: { open: false } });

    server.on("GET", "/api/v0/registration", () => data({ open: true }));

    expect(await read("/api/v0/registration")).toEqual({ data: { open: true } });
    expect(server.unknown).toEqual([]);
  });

  it("answers the session, and once it ends a 401 to all but a visitor's operations", async () => {
    const server = fakeServer(memberSession);
    server.on("GET", "/api/v0/places", () => data({ places: [] }));
    const session = await server.fetch(`${origin}/api/v0/sessions/current`);
    expect(await session.json()).toEqual({ data: memberSession });

    server.session = undefined;

    const ended = await server.fetch(`${origin}/api/v0/sessions/current`);
    expect(ended.status).toBe(401);
    expect(await ended.json()).toMatchObject({ type: "/api/v0/problems/unauthenticated" });
    expect((await server.fetch(`${origin}/api/v0/places`)).status).toBe(401);
    const setup = await server.fetch(`${origin}/api/v0/setup`);
    expect(await setup.json()).toEqual({ data: { status: "done" } });
  });

  it("rejects as a dropped connection does when a handler loses the answer", async () => {
    const server = fakeServer(memberSession);
    let acted = false;
    server.on("POST", "/api/v0/labels", () => {
      acted = true;
      return lose();
    });

    await expect(server.fetch(`${origin}/api/v0/labels`, { method: "POST" })).rejects.toThrow(
      new TypeError("Failed to fetch"),
    );
    expect(acted).toBe(true);
  });

  it("takes only the contract's paths and methods", () => {
    const server = fakeServer();
    // @ts-expect-error -- not a path of the API.
    server.on("GET", "/api/v0/place", () => data(null));
    // @ts-expect-error -- Places has no POST.
    server.on("POST", "/api/v0/places", () => data(null));
    expect(server.requests).toEqual([]);
  });
});

describe("expectAllAnswered", () => {
  it("throws naming each request an installed server did not know, once", async () => {
    fakeServer(memberSession).install();
    await fetch(`${origin}/api/v0/places?x=1`);
    await fetch(`${origin}/api/v0/labels`, { method: "POST" });

    expect(expectAllAnswered).toThrow(
      new Error("Requests without an answer: GET /api/v0/places?x=1, POST /api/v0/labels"),
    );
    // The servers are forgotten, so the next test starts clean.
    expect(expectAllAnswered).not.toThrow();
  });

  it("passes when every request had a route, and for a server not installed", async () => {
    const installed = fakeServer(memberSession).install();
    installed.on("GET", "/api/v0/places", () => problem(503, "/api/v0/problems/service-busy"));
    await fetch(`${origin}/api/v0/places`);
    await fakeServer(memberSession).fetch(`${origin}/api/v0/places`);

    expect(expectAllAnswered).not.toThrow();
  });
});

describe("problem", () => {
  it("sends the required members, and Retry-After when given", async () => {
    const answer = problem(429, "/api/v0/problems/too-many-requests", {
      retryAfter: 30,
      detail: "Wait.",
    });

    expect(answer.status).toBe(429);
    expect(answer.headers.get("Retry-After")).toBe("30");
    expect(await answer.json()).toEqual({
      type: "/api/v0/problems/too-many-requests",
      status: 429,
      title: "Failed",
      detail: "Wait.",
    });
  });
});
