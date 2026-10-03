import { describe, expect, it } from "vite-plus/test";
import { summarize, userMessage } from "./problem";
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
