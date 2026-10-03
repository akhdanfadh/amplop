import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { ResponseHealthStatus } from "./generated/model";
import { ApiError, transport } from "./transport";

const url = "/api/v0/health";
const init = { schema: ResponseHealthStatus };

// Makes the next fetch answer with this body, status, and content type.
function answer(body: BodyInit | null, status: number, type: string, statusText = "") {
  vi.stubGlobal(
    "fetch",
    async () => new Response(body, { status, statusText, headers: { "Content-Type": type } }),
  );
}

// Silences and records the transport's report of a contract mismatch.
function watchLog() {
  return vi.spyOn(console, "error").mockImplementation(() => {});
}

// Sends the request and returns what it threw.
async function failure() {
  const err: unknown = await transport(url, init).catch((e: unknown) => e);
  if (!(err instanceof ApiError)) throw new Error(`not an ApiError: ${String(err)}`);
  return err;
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("transport", () => {
  it("returns a body that matches the schema", async () => {
    answer(JSON.stringify({ data: { status: "ok" } }), 200, "application/json");

    await expect(transport(url, init)).resolves.toEqual({ data: { status: "ok" } });
  });

  it("labels a request with no response unreachable", async () => {
    vi.stubGlobal("fetch", async () => {
      throw new TypeError("Failed to fetch");
    });

    const err = await failure();
    expect(err.kind).toBe("unreachable");
    expect(err.problem).toEqual({ title: "Could not reach the server" });
  });

  it("labels Go's Problem Details with their status", async () => {
    const problem = { status: 409, title: "Conflict", detail: "counter is full" };
    answer(JSON.stringify(problem), 409, "application/problem+json");

    const err = await failure();
    expect(err.kind).toBe("status");
    // The schema fills in the default type.
    expect(err.problem).toEqual({ ...problem, type: "about:blank" });
  });

  it("labels the proxy's plain-text 502 with its status", async () => {
    answer("Bad Gateway", 502, "text/plain");

    const err = await failure();
    expect(err.kind).toBe("status");
    expect(err.problem.status).toBe(502);
  });

  it("labels a body that breaks the contract unexpected", async () => {
    const log = watchLog();
    answer("<html></html>", 200, "text/html");

    const err = await failure();
    expect(err.kind).toBe("unexpected");
    expect(err.problem).toEqual({ title: "Unexpected response from the server" });
    expect(log).toHaveBeenCalledOnce();
  });

  it("keeps the status when Problem Details break the contract", async () => {
    const log = watchLog();
    // A relative type fails the schema, which wants an absolute URI.
    const problem = { type: "/problems/taken", status: 409, title: "Conflict" };
    answer(JSON.stringify(problem), 409, "application/problem+json", "Conflict");

    const err = await failure();
    expect(err.kind).toBe("status");
    expect(err.problem).toEqual({ status: 409, title: "Conflict" });
    expect(log).toHaveBeenCalledOnce();
  });
});
