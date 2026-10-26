import { describe, expect, it } from "vite-plus/test";
import { formErrors, userMessage } from "./problem";
import { ApiError } from "./transport";

const httpStatus = {
  created: 201,
  conflict: 409,
  unprocessable: 422,
  internalServerError: 500,
  badGateway: 502,
  unavailable: 503,
  gatewayTimeout: 504,
};

describe("userMessage", () => {
  it("explains a read timeout without calling it an unreachable server", () => {
    expect(userMessage(new ApiError("timeout", { method: "GET" }))).toBe(
      "The request took too long. Try again.",
    );
  });

  it("keeps the write warning when waiting for confirmation times out", () => {
    expect(userMessage(new ApiError("timeout", { method: "POST" }))).toBe(
      "We couldn't confirm whether this was saved. Check before submitting again.",
    );
  });

  it("preserves reported write success after a body-read timeout", () => {
    const err = new ApiError("timeout", { method: "POST", status: httpStatus.created });

    expect(userMessage(err)).toBe(
      "The server reported success, but we couldn't load the result. Check before submitting again.",
    );
  });

  it("keeps a known rejection when reading its explanation times out", () => {
    const err = new ApiError("timeout", { method: "POST", status: httpStatus.unprocessable });

    expect(userMessage(err)).toBe("Check the values and try again.");
  });

  it.each(["POST", "PUT", "PATCH", "DELETE"])("warns when a %s outcome is unknown", (method) => {
    const err = new ApiError("network", { method });

    expect(userMessage(err)).toBe(
      "We couldn't confirm whether this was saved. Check before submitting again.",
    );
  });

  it("distinguishes a reported write success from an unreadable result", () => {
    const err = new ApiError("response", { method: "POST", status: httpStatus.created });

    expect(userMessage(err)).toBe(
      "The server reported success, but we couldn't load the result. Check before submitting again.",
    );
  });

  it("keeps a successful write's status when its body is interrupted", () => {
    const err = new ApiError("network", { method: "POST", status: httpStatus.created });

    expect(userMessage(err)).toBe(
      "The server reported success, but we couldn't load the result. Check before submitting again.",
    );
  });

  it.each([
    httpStatus.internalServerError,
    httpStatus.badGateway,
    httpStatus.unavailable,
    httpStatus.gatewayTimeout,
  ])("does not assume a write rolled back after HTTP %s", (status) => {
    const err = new ApiError("http", { method: "POST", status });

    expect(userMessage(err)).toBe(
      "We couldn't confirm whether this was saved. Check before submitting again.",
    );
  });

  it("preserves safe server text alongside the uncertain write warning", () => {
    const err = new ApiError("http", {
      method: "POST",
      status: httpStatus.unavailable,
      problem: { detail: "The service is restarting." },
    });

    expect(userMessage(err)).toBe(
      "The service is restarting. We couldn't confirm whether this was saved. Check before submitting again.",
    );
  });

  it("keeps a known write rejection's explanation", () => {
    const err = new ApiError("http", {
      method: "POST",
      status: httpStatus.conflict,
      problem: { detail: "The counter is full." },
    });

    expect(userMessage(err)).toBe("The counter is full.");
  });

  it("keeps a write rejection when its body is interrupted", () => {
    const err = new ApiError("network", { method: "POST", status: httpStatus.unprocessable });

    expect(userMessage(err)).toBe("Check the values and try again.");
  });

  it("prefers the safe detail over the title", () => {
    const err = new ApiError("http", {
      status: 404,
      problem: { title: "Not Found", detail: "route not found" },
    });

    expect(userMessage(err)).toBe("route not found");
  });

  it("shows the title alone without a detail", () => {
    const err = new ApiError("http", { status: 502, problem: { title: "Bad Gateway" } });

    expect(userMessage(err)).toBe("Bad Gateway");
  });

  it("uses local wording when no problem arrived", () => {
    expect(userMessage(new ApiError("network"))).toBe("Can't reach the server. Try again.");
  });

  it("uses local wording when the received explanation is blank", () => {
    const err = new ApiError("http", {
      status: 422,
      problem: { title: " ", detail: "\t" },
    });

    expect(userMessage(err)).toBe("Check the values and try again.");
  });

  it.each([
    ["no response", new ApiError("network"), "Can't reach the server. Try again."],
    [
      "the proxy's 502",
      new ApiError("http", { status: 502 }),
      "Can't reach the server. Try again.",
    ],
    ["rejected values", new ApiError("http", { status: 422 }), "Check the values and try again."],
    [
      "a server failure",
      new ApiError("http", { status: 500 }),
      "Something went wrong on our side. Try again.",
    ],
    [
      "a timeout behind a proxy",
      new ApiError("http", { status: 504 }),
      "Something went wrong on our side. Try again.",
    ],
    ["a contract mismatch", new ApiError("response"), "Something went wrong. Try again."],
    [
      "a conflict without an explanation",
      new ApiError("http", { status: 409 }),
      "Something went wrong. Try again.",
    ],
    [
      "a safe detail for an unfamiliar problem",
      new ApiError("http", {
        status: 409,
        problem: { type: "/api/v0/problems/other-conflict", detail: "Choose another name." },
      }),
      "Choose another name.",
    ],
    [
      "actual server status despite a different problem status",
      new ApiError("http", { status: 503, problem: { status: 422 } }),
      "Something went wrong on our side. Try again.",
    ],
  ])("words %s for the user", (_case, err, message) => {
    expect(userMessage(err)).toBe(message);
  });
});

// A 422 from Go with these errors[] entries.
function unprocessable(errors: { location: string; message: string }[]): ApiError {
  return new ApiError("http", { status: 422, problem: { errors } });
}

describe("formErrors", () => {
  it("keeps safe messages on rendered fields and returns unmatched messages for the summary", () => {
    const err = unprocessable([
      { location: "body.note", message: "Use at most 120 characters." },
      { location: "body.amount", message: "Enter a whole number from 1 to 10." },
      { location: "body.owner", message: "Choose an owner." },
      { location: "query.page", message: "Enter a whole number." },
    ]);

    expect(formErrors(err, ["note", "amount"])).toEqual({
      fields: {
        note: [{ message: "Use at most 120 characters." }],
        amount: [{ message: "Enter a whole number from 1 to 10." }],
      },
      unmatched: [{ message: "Choose an owner." }, { message: "Enter a whole number." }],
    });
  });

  it("matches nested paths exactly and preserves several messages per field", () => {
    const err = unprocessable([
      { location: "body.items[3].tags", message: "Choose at least one tag." },
      { location: "body.items[3].tags", message: "Choose no more than four tags." },
      { location: "body.items[3].name", message: "This field is required." },
    ]);

    expect(formErrors(err, ["items[3].tags"])).toEqual({
      fields: {
        "items[3].tags": [
          { message: "Choose at least one tag." },
          { message: "Choose no more than four tags." },
        ],
      },
      unmatched: [{ message: "This field is required." }],
    });
  });

  it("keeps whole-body errors in the summary and gives blank messages a fallback", () => {
    const err = unprocessable([
      { location: "body", message: "Correct the submitted input." },
      { location: "body.note", message: " " },
    ]);

    expect(formErrors(err, ["note"])).toEqual({
      fields: { note: [{ message: "This value was not accepted." }] },
      unmatched: [{ message: "Correct the submitted input." }],
    });
  });

  it("maps explicit business input errors even on a 409", () => {
    const err = new ApiError("http", {
      status: 409,
      problem: { errors: [{ location: "body.note", message: "Choose another note." }] },
    });

    expect(formErrors(err, ["note"])).toEqual({
      fields: { note: [{ message: "Choose another note." }] },
      unmatched: [],
    });
  });

  it("has no input errors for a temporary request failure", () => {
    const err = new ApiError("http", {
      status: 503,
      problem: { detail: "The service is restarting." },
    });

    expect(formErrors(err, ["note"])).toEqual({ fields: {}, unmatched: [] });
  });
});
