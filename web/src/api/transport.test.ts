import { afterEach, describe, expect, it, vi, type MockInstance } from "vite-plus/test";
import { ResponseHealthStatus } from "./generated/model";
import { ApiError, transport } from "./transport";

const url = "/api/v0/health";
const init = { schema: ResponseHealthStatus };
const httpStatus = {
  ok: 200,
  conflict: 409,
  unprocessable: 422,
  badGateway: 502,
  unavailable: 503,
};

// Makes the next fetch answer with this body, status, and content type.
function answer(body: BodyInit | null, status: number, type: string, statusText = ""): void {
  vi.stubGlobal(
    "fetch",
    async () => new Response(body, { status, statusText, headers: { "Content-Type": type } }),
  );
}

// Silences and records the transport's report of a contract mismatch.
function watchLog(): MockInstance<typeof console.error> {
  return vi.spyOn(console, "error").mockImplementation(() => {});
}

// Sends the request and returns what it threw.
async function failure(): Promise<ApiError> {
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

  it("labels a request with no response a network failure", async () => {
    vi.stubGlobal("fetch", async () => {
      throw new TypeError("Failed to fetch");
    });

    const err = await failure();
    expect(err.kind).toBe("network");
    expect(err.problem).toBeUndefined();
    expect(err.status).toBeUndefined();
  });

  it("labels Go's Problem Details with their status", async () => {
    const problem = { status: 409, title: "Conflict", detail: "counter is full" };
    answer(JSON.stringify(problem), 409, "application/problem+json");

    const err = await failure();
    expect(err.kind).toBe("http");
    expect(err.status).toBe(409);
    expect(err.problem).toEqual(problem);
  });

  it("labels the proxy's plain-text 502 with its status", async () => {
    answer("Bad Gateway", 502, "text/plain");

    const err = await failure();
    expect(err.kind).toBe("http");
    expect(err.status).toBe(502);
    expect(err.problem).toBeUndefined();
  });

  it("labels a body that breaks the contract a response failure", async () => {
    const log = watchLog();
    answer("<html></html>", 200, "text/html");

    const err = await failure();
    expect(err.kind).toBe("response");
    expect(err.status).toBe(200);
    expect(err.problem).toBeUndefined();
    expect(log).toHaveBeenCalledOnce();
  });

  it("requires the exact problem media type", async () => {
    const problem = { type: "/problems/taken", status: 409, title: "Conflict" };
    answer(JSON.stringify(problem), 409, "application/problem+json-extra", "Conflict");

    const err = await failure();
    expect(err.kind).toBe("http");
    expect(err.status).toBe(409);
    expect(err.problem).toBeUndefined();
  });

  it("labels JSON of the wrong shape a response failure", async () => {
    const log = watchLog();
    answer(JSON.stringify({ openapi: "3.1.0" }), 200, "application/json");

    const err = await failure();
    expect(err.kind).toBe("response");
    expect(log).toHaveBeenCalledOnce();
  });

  it("drops fields the schema does not know", async () => {
    answer(JSON.stringify({ data: { status: "ok", uptime: 42 } }), 200, "application/json");

    await expect(transport(url, init)).resolves.toEqual({ data: { status: "ok" } });
  });

  it("rethrows a request the caller cancelled as is", async () => {
    const cancelled = new DOMException("The operation was aborted.", "AbortError");
    vi.stubGlobal("fetch", async () => {
      throw cancelled;
    });
    const caller = new AbortController();
    caller.abort();

    await expect(transport(url, { ...init, signal: caller.signal })).rejects.toBe(cancelled);
  });

  it("sends nothing for an operation without a response schema", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>();
    vi.stubGlobal("fetch", fetch);

    await expect(transport(url, {})).rejects.toThrow(`${url}: no response schema`);
    expect(fetch).not.toHaveBeenCalled();
  });
});

describe("received error facts", () => {
  it("keeps actual status separate from the received problem and request ID", async () => {
    const problem = {
      type: "/api/v0/problems/counter-full",
      title: "Counter full",
      detail: "The counter is full.",
      status: httpStatus.conflict,
    };
    vi.stubGlobal(
      "fetch",
      async () =>
        new Response(JSON.stringify(problem), {
          status: httpStatus.unavailable,
          headers: {
            "Content-Type": "application/problem+json; charset=utf-8",
            "X-Request-ID": "request-reference",
          },
        }),
    );
    const error = await failure();
    expect(error.kind).toBe("http");
    expect(error.status).toBe(httpStatus.unavailable);
    expect(error.problem).toEqual(problem);
    expect(error.requestId).toBe("request-reference");
  });

  it("accepts an external problem without fabricating absent members", async () => {
    const problem = {
      detail: "Maintenance is in progress.",
      type: "https://proxy.example/problems/maintenance",
    };
    vi.stubGlobal(
      "fetch",
      async () =>
        new Response(JSON.stringify(problem), {
          status: httpStatus.unavailable,
          headers: { "Content-Type": "application/problem+json" },
        }),
    );
    expect((await failure()).problem).toEqual(problem);
  });

  it.each([
    { body: "<html>Private proxy diagnostics</html>", type: "text/html" },
    { body: "{", type: "application/problem+json" },
    { body: "null", type: "application/problem+json" },
    { body: "[]", type: "application/problem+json" },
  ])("keeps unusable error body $body as HTTP facts without a problem", async ({ body, type }) => {
    vi.stubGlobal(
      "fetch",
      async () =>
        new Response(body, {
          status: httpStatus.badGateway,
          headers: { "X-Request-ID": "proxy-reference", "Content-Type": type },
        }),
    );
    const error = await failure();
    expect(error.kind).toBe("http");
    expect(error.status).toBe(httpStatus.badGateway);
    expect(error.problem).toBeUndefined();
    expect(error.requestId).toBe("proxy-reference");
    expect(error.message).not.toContain("Private proxy diagnostics");
  });

  it.each([httpStatus.ok, httpStatus.unprocessable, httpStatus.unavailable])(
    "retains status %s and reference when body reading fails",
    async (status) => {
      vi.stubGlobal(
        "fetch",
        async () =>
          new Response(
            new ReadableStream({
              start(controller) {
                controller.error(new TypeError("connection lost"));
              },
            }),
            {
              status,
              headers: {
                "Content-Type": "application/problem+json",
                "X-Request-ID": "body-reference",
              },
            },
          ),
      );
      const error = await failure();
      expect(error.kind).toBe("network");
      expect(error.status).toBe(status);
      expect(error.requestId).toBe("body-reference");
      expect(error.problem).toBeUndefined();
    },
  );

  it("ignores incorrectly typed standard members but keeps usable detail", async () => {
    vi.stubGlobal(
      "fetch",
      async () =>
        new Response(
          JSON.stringify({
            status: "503",
            title: [],
            type: "bad reference",
            detail: "Try again later.",
          }),
          {
            status: httpStatus.unavailable,
            headers: { "Content-Type": "application/problem+json" },
          },
        ),
    );
    const error = await failure();
    expect(error.status).toBe(httpStatus.unavailable);
    expect(error.problem).toEqual({ detail: "Try again later." });
  });

  it("does not mistake a caller timeout for deliberate cancellation", async () => {
    const timeout = new DOMException("Time budget exceeded", "TimeoutError");
    const caller = new AbortController();
    caller.abort(timeout);
    vi.stubGlobal("fetch", async () => {
      throw timeout;
    });
    await expect(transport(url, { ...init, signal: caller.signal })).rejects.toMatchObject({
      kind: "network",
    });
  });

  it("keeps deliberate cancellation during body reading outside ApiError", async () => {
    const cancelled = new DOMException("Obsolete request", "AbortError");
    const caller = new AbortController();
    vi.stubGlobal(
      "fetch",
      async () =>
        new Response(
          new ReadableStream({
            start(controller) {
              caller.abort();
              controller.error(cancelled);
            },
          }),
          { status: httpStatus.ok },
        ),
    );
    await expect(transport(url, { ...init, signal: caller.signal })).rejects.toBe(cancelled);
  });
});
