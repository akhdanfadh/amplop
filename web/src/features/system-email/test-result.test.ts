import { describe, expect, it } from "vite-plus/test";
import { ApiError } from "@/api/transport";
import { testErrorMessage, testResultMessage } from "./test-result";

describe("testResultMessage", () => {
  it("names the recipient of an accepted message", () => {
    expect(testResultMessage({ status: "accepted" }, "admin@example.com")).toBe(
      "The mail server accepted the test message. Check the inbox of admin@example.com.",
    );
  });

  it.each([
    ["connect", "Check the host and port."],
    ["tls", "Check the security setting and port."],
    ["auth", "Check the username and password."],
    ["sender", "Check the From address."],
    ["recipient", "Check the recipient address."],
    ["timeout", "Check the host and port, then try again."],
  ] as const)("points a %s failure at its settings", (reason, advice) => {
    expect(testResultMessage({ status: "failed", reason }, "admin@example.com")).toContain(advice);
  });

  it("words a failure without a reason", () => {
    expect(testResultMessage({ status: "failed" }, "admin@example.com")).toBe(
      "The test message failed. Check the settings and try again.",
    );
  });
});

describe("testErrorMessage", () => {
  it.each([
    ["no response", new ApiError("network", { method: "POST" })],
    ["a timeout", new ApiError("timeout", { method: "POST" })],
    ["a server error", new ApiError("http", { method: "POST", status: 500 })],
    ["an unreadable result", new ApiError("response", { method: "POST", status: 200 })],
  ])("offers another test after %s", (_, err) => {
    expect(testErrorMessage(err)).toBe(
      "We couldn't get the test result. Send another test to check the settings.",
    );
  });

  it("keeps the server's explanation of a lost result", () => {
    const err = new ApiError("http", {
      method: "POST",
      status: 503,
      problem: { detail: "The server is restarting." },
    });
    expect(testErrorMessage(err)).toBe(
      "The server is restarting. We couldn't get the test result. Send another test to check the settings.",
    );
  });

  it("keeps the server's safe text", () => {
    const err = new ApiError("http", {
      method: "POST",
      status: 409,
      problem: { detail: "Save the system email settings before sending a test message." },
    });
    expect(testErrorMessage(err)).toBe(
      "Save the system email settings before sending a test message.",
    );
  });
});
