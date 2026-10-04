import { describe, expect, it } from "vite-plus/test";
import { fieldErrors, summarize, userMessage } from "./problem";
import { ApiError } from "./transport";

describe("summarize", () => {
  it("joins the title and detail", () => {
    const err = new ApiError("status", {
      status: 404,
      title: "Not Found",
      detail: "route not found",
    });

    expect(summarize(err)).toBe("Not Found: route not found");
  });

  it("shows the title alone without a detail", () => {
    const err = new ApiError("status", { status: 502, title: "Bad Gateway" });

    expect(summarize(err)).toBe("Bad Gateway");
  });
});

describe("userMessage", () => {
  it.each([
    [
      "no response",
      new ApiError("unreachable", { title: "Could not reach the server" }),
      "Can't reach the server. Try again.",
    ],
    [
      "the proxy's 502",
      new ApiError("status", { status: 502, title: "Bad Gateway" }),
      "Can't reach the server. Try again.",
    ],
    [
      "rejected values",
      new ApiError("status", { status: 422, title: "Unprocessable Entity" }),
      "Check the values and try again.",
    ],
    [
      "a server failure",
      new ApiError("status", { status: 500, title: "Internal Server Error" }),
      "Something went wrong on our side. Try again.",
    ],
    [
      "a timeout behind a proxy",
      new ApiError("status", { status: 504, title: "Gateway Timeout" }),
      "Something went wrong on our side. Try again.",
    ],
    [
      "a contract mismatch",
      new ApiError("unexpected", { title: "Unexpected response from the server" }),
      "Something went wrong. Try again.",
    ],
    [
      "any other status",
      new ApiError("status", { status: 409, title: "Conflict", detail: "counter is full" }),
      "Something went wrong. Try again.",
    ],
  ])("words %s for the user", (_case, err, message) => {
    expect(userMessage(err)).toBe(message);
  });
});

// A 422 from Go with these errors[] entries.
function unprocessable(errors: { location?: string; message?: string }[]) {
  return new ApiError("status", { status: 422, title: "Unprocessable Entity", errors });
}

describe("fieldErrors", () => {
  const rejected = { message: "This value was not accepted." };

  it("puts each body error on its field, in the app's words", () => {
    const err = unprocessable([
      { location: "body.note", message: "expected string to be not blank" },
      { location: "body.amount", message: "expected number >= 1" },
    ]);

    expect(fieldErrors(err)).toEqual({ note: rejected, amount: rejected });
  });

  it("keeps a nested path as the field name", () => {
    const err = unprocessable([{ location: "body.items[3].tags", message: "expected array" }]);

    expect(fieldErrors(err)).toEqual({ "items[3].tags": rejected });
  });

  it("gives a field one message for several errors", () => {
    const err = unprocessable([
      { location: "body.note", message: "expected string to be not blank" },
      { location: "body.note", message: "expected length <= 120" },
    ]);

    expect(fieldErrors(err)).toEqual({ note: rejected });
  });

  it("skips errors outside the body's fields", () => {
    const err = unprocessable([
      { location: "body", message: "expected required property note to be present" },
      { location: "query.page", message: "expected integer" },
      { message: "no location" },
    ]);

    expect(fieldErrors(err)).toEqual({});
  });

  it("has nothing for a failure without errors", () => {
    const err = new ApiError("status", {
      status: 409,
      title: "Conflict",
      detail: "counter is full",
    });

    expect(fieldErrors(err)).toEqual({});
  });
});
