import { describe, expect, it } from "vite-plus/test";
import { ApiError } from "@/api/transport";
import { alreadyVerified, linkInvalid, redeemErrorMessage, sendErrorMessage } from "./text";

const alreadyVerifiedProblem = {
  type: "/api/v0/problems/email-already-verified",
  detail: "Your email address is already verified.",
};
const notConfiguredProblem = {
  type: "/api/v0/problems/system-email-not-configured",
  detail: "Email can't be sent until an administrator sets up system email.",
};
const linkInvalidProblem = {
  type: "/api/v0/problems/email-verification-link-invalid",
  detail: "This link has expired, was already used, or is not valid. Ask for a new one.",
};

describe("sendErrorMessage", () => {
  it.each([
    ["no response", new ApiError("network", { method: "POST" })],
    ["a timeout", new ApiError("timeout", { method: "POST" })],
    ["an unreadable result", new ApiError("response", { method: "POST", status: 200 })],
  ])("offers another link after %s", (_, err) => {
    expect(sendErrorMessage(err)).toBe(
      "We couldn't confirm whether the link was sent. If no email arrives in a few minutes, send another link.",
    );
  });

  // Go saves the link before sending, so even a failed send may deliver it.
  it("says a link from a failed send still works", () => {
    const err = new ApiError("http", {
      method: "POST",
      status: 502,
      problem: { detail: "The email could not be sent. Try again later." },
    });
    expect(sendErrorMessage(err)).toBe(
      "The email could not be sent. Try again later. If an email still arrives, its link works.",
    );
  });

  it.each([
    [
      "too many links",
      429,
      { detail: "Too many verification links were sent recently. Try again later." },
    ],
    ["no system email", 409, notConfiguredProblem],
    ["an already verified address", 409, alreadyVerifiedProblem],
  ])("keeps the server's text for %s", (_, status, problem) => {
    const err = new ApiError("http", { method: "POST", status, problem });
    expect(sendErrorMessage(err)).toBe(problem.detail);
  });
});

describe("alreadyVerified", () => {
  it("tells the two conflicts apart by type", () => {
    const verified = new ApiError("http", { status: 409, problem: alreadyVerifiedProblem });
    const notConfigured = new ApiError("http", { status: 409, problem: notConfiguredProblem });

    expect(alreadyVerified(verified)).toBe(true);
    expect(alreadyVerified(notConfigured)).toBe(false);
    expect(alreadyVerified(new ApiError("http", { status: 409 }))).toBe(false);
  });
});

describe("redeemErrorMessage", () => {
  const invalid = new ApiError("http", {
    method: "POST",
    status: 404,
    problem: linkInvalidProblem,
  });

  it("words a refused link", () => {
    expect(redeemErrorMessage(invalid, false)).toBe(linkInvalidProblem.detail);
  });

  it("says the address is verified when the signed-in account already is", () => {
    expect(redeemErrorMessage(invalid, true)).toBe(
      "This link is no longer valid, but your email address is already verified.",
    );
  });

  // Only the API's own 404 says the link is gone; a missing route says nothing.
  it("treats another 404 as an ordinary failure", () => {
    const err = new ApiError("http", { method: "POST", status: 404 });
    expect(redeemErrorMessage(err, true)).toBe("Something went wrong. Try again.");
  });

  it("keeps the server's explanation of an uncertain outcome", () => {
    const err = new ApiError("http", {
      method: "POST",
      status: 503,
      problem: { detail: "The server is restarting." },
    });
    expect(redeemErrorMessage(err, false)).toBe(
      "The server is restarting. We couldn't confirm whether your email address was verified. Try again.",
    );
  });

  it.each([
    ["no response", new ApiError("network", { method: "POST" })],
    ["a server error", new ApiError("http", { method: "POST", status: 500 })],
  ])("asks for another try after %s", (_, err) => {
    expect(redeemErrorMessage(err, false)).toBe(
      "We couldn't confirm whether your email address was verified. Try again.",
    );
  });
});

describe("linkInvalid", () => {
  it("accepts only the API's own 404", () => {
    expect(linkInvalid(new ApiError("http", { status: 404, problem: linkInvalidProblem }))).toBe(
      true,
    );
    expect(linkInvalid(new ApiError("http", { status: 404 }))).toBe(false);
  });
});
