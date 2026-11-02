import { describe, expect, it } from "vite-plus/test";
import type { ProblemType } from "@/api/generated/model";
import { ApiError } from "@/api/transport";
import { registrationClosed, requestedMessage, requestErrorMessage } from "./visitor-text";

// refusal returns a received problem of type with status.
function refusal(method: string, status: number, type: ProblemType, detail: string): ApiError {
  return new ApiError("http", { method, status, problem: { type, detail } });
}

describe("requestedMessage", () => {
  // ADR028: the same answer whether or not the email has an account.
  it("names the inbox without saying what was sent", () => {
    expect(requestedMessage("kate@example.org")).toBe(
      "Check your inbox at kate@example.org. A sign-up link expires in 24 hours. If nothing arrives in a few minutes, check your spam folder.",
    );
  });
});

describe("registrationClosed", () => {
  it("finds a refusal because registration is not open", () => {
    const err = refusal(
      "POST",
      409,
      "/api/v0/problems/registration-closed",
      "Registration is not open right now.",
    );

    expect(registrationClosed(err)).toBe(true);
  });

  it("ignores other conflicts", () => {
    const err = refusal(
      "POST",
      409,
      "/api/v0/problems/registration-account-exists",
      "This email already has an account. Sign in instead.",
    );

    expect(registrationClosed(err)).toBe(false);
  });
});

describe("requestErrorMessage", () => {
  it("keeps the server's text for a refusal", () => {
    const err = refusal(
      "POST",
      409,
      "/api/v0/problems/registration-closed",
      "Registration is not open right now.",
    );

    expect(requestErrorMessage(err)).toBe("Registration is not open right now.");
  });

  // Go saves the link before sending it, so a failed send may still deliver.
  it("says a link from a failed send still works", () => {
    const err = refusal(
      "POST",
      502,
      "/api/v0/problems/email-not-sent",
      "The email could not be sent. Try again later.",
    );

    expect(requestErrorMessage(err)).toBe(
      "The email could not be sent. Try again later. If an email still arrives, its link works.",
    );
  });

  it("offers another try when the outcome is unknown", () => {
    expect(requestErrorMessage(new ApiError("network", { method: "POST" }))).toBe(
      "We couldn't confirm whether the email was sent. If nothing arrives in a few minutes, try again.",
    );
  });
});
