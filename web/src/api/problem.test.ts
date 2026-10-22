import { describe, expect, it } from "vite-plus/test";
import { fieldErrors, summarize, userMessage } from "./problem";
import { ApiError } from "./transport";

describe("summarize", () => {
  it("joins the title and detail", () => {
    const err = new ApiError("http", {
      status: 404,
      problem: { title: "Not Found", detail: "route not found" },
    });

    expect(summarize(err)).toBe("Not Found: route not found");
  });

  it("shows the title alone without a detail", () => {
    const err = new ApiError("http", { status: 502, problem: { title: "Bad Gateway" } });

    expect(summarize(err)).toBe("Bad Gateway");
  });

  it("uses local wording when no problem arrived", () => {
    expect(summarize(new ApiError("network"))).toBe("Can't reach the server. Try again.");
  });
});

describe("userMessage", () => {
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
      "any other status",
      new ApiError("http", { status: 409, problem: { detail: "counter is full" } }),
      "Something went wrong. Try again.",
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
    ]);

    expect(fieldErrors(err)).toEqual({});
  });

  it("has nothing for a failure without errors", () => {
    const err = new ApiError("http", { status: 409 });

    expect(fieldErrors(err)).toEqual({});
  });
});
