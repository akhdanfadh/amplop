import { describe, expect, it } from "vite-plus/test";
import type { ProblemType } from "@/api/generated/model";
import { ApiError } from "@/api/transport";
import { requestedMessage, requestErrorMessage } from "./text";

// refusal returns a received problem of type with status.
function refusal(status: number, type: ProblemType, detail?: string): ApiError {
  return new ApiError("http", { method: "POST", status, problem: { type, detail } });
}

describe("requestedMessage", () => {
  // ADR030: the same answer whether or not the email has an account.
  it("names the inbox without saying whether a link was sent", () => {
    expect(requestedMessage("kate@example.org")).toBe(
      "If kate@example.org is the verified address of an active account, a reset link is on its way. It can take a few minutes to arrive and expires in 1 hour. If nothing arrives, check your spam folder.",
    );
  });
});

describe("requestErrorMessage", () => {
  it("keeps the server's text for a refusal", () => {
    const err = refusal(
      422,
      "/api/v0/problems/validation-error",
      "The request contains invalid values.",
    );

    expect(requestErrorMessage(err)).toBe("The request contains invalid values.");
  });

  it("offers another try when the outcome is unknown", () => {
    expect(requestErrorMessage(new ApiError("network", { method: "POST" }))).toBe(
      "We couldn't confirm whether your request went through. If no email arrives in a few minutes, try again.",
    );
  });
});
