import { describe, expect, it } from "vite-plus/test";
import type { ProblemType } from "@/api/generated/model";
import { ApiError } from "@/api/transport";
import {
  completeEnd,
  completeErrorMessage,
  endOf,
  registrationClosed,
  requestedMessage,
  requestErrorMessage,
} from "./visitor-text";

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

describe("completeEnd", () => {
  it.each([
    [
      404,
      "/api/v0/problems/registration-link-invalid",
      "This link has expired, was already used, or is not valid. Ask for a new one.",
      "link-invalid",
    ],
    [
      409,
      "/api/v0/problems/registration-account-exists",
      "This email already has an account. Sign in instead.",
      "account-exists",
    ],
  ] as const)("ends the page on %i %s", (status, type, detail, end) => {
    expect(completeEnd(refusal("POST", status, type, detail))).toBe(end);
  });

  // Pausing keeps links, so the same link may work later.
  it("keeps the form when registration is closed", () => {
    const err = refusal(
      "POST",
      409,
      "/api/v0/problems/registration-closed",
      "Registration is not open right now.",
    );

    expect(completeEnd(err)).toBeUndefined();
  });
});

describe("completeErrorMessage", () => {
  it("keeps the server's text for a refusal", () => {
    const err = refusal(
      "POST",
      404,
      "/api/v0/problems/registration-link-invalid",
      "This link has expired, was already used, or is not valid. Ask for a new one.",
    );

    expect(completeErrorMessage(err)).toBe(
      "This link has expired, was already used, or is not valid. Ask for a new one.",
    );
  });

  // A created account deletes its link, so a second try reads "not valid".
  it("says how to tell whether an unknown try created the account", () => {
    expect(completeErrorMessage(new ApiError("network", { method: "POST" }))).toBe(
      "We couldn't confirm whether your account was created. Try again. If the link is then no longer valid, sign in with this password.",
    );
  });
});

describe("endOf", () => {
  const spent = refusal(
    "POST",
    404,
    "/api/v0/problems/registration-link-invalid",
    "This link has expired, was already used, or is not valid. Ask for a new one.",
  );

  it("offers a new link for a link that no longer works", () => {
    expect(endOf(spent, "link-invalid", "refused")).toEqual({
      message: "This link has expired, was already used, or is not valid. Ask for a new one.",
      links: ["new-link", "sign-in"],
    });
  });

  // A created account deletes its link, so the unknown try likely worked.
  it("points to sign-in after a try with an unknown outcome", () => {
    expect(endOf(spent, "link-invalid", "unknown")).toEqual({
      message:
        "This link no longer works, so an earlier try probably created your account. Sign in with the password you sent then. If you're not sure which one that was, reset your password from the sign-in page.",
      links: ["sign-in", "new-link"],
    });
  });

  it("points to sign-in for an email with an account", () => {
    const err = refusal(
      "POST",
      409,
      "/api/v0/problems/registration-account-exists",
      "This email already has an account. Sign in instead.",
    );

    expect(endOf(err, "account-exists", "unknown")).toEqual({
      message: "This email already has an account. Sign in instead.",
      links: ["sign-in"],
    });
  });
});
