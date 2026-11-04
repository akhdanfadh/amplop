import { describe, expect, it } from "vite-plus/test";
import type { ProblemType } from "@/api/generated/model";
import { ApiError } from "@/api/transport";
import {
  completeErrorMessage,
  endOf,
  linkInvalid,
  requestedMessage,
  requestErrorMessage,
} from "./text";

const linkInvalidType: ProblemType = "/api/v0/problems/password-reset-link-invalid";
const linkInvalidDetail =
  "This link has expired, was already used, or is not valid. Ask for a new one.";

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

describe("linkInvalid", () => {
  it("finds a link that no longer works", () => {
    expect(linkInvalid(refusal(404, linkInvalidType, linkInvalidDetail))).toBe(true);
  });

  it("ignores other refusals", () => {
    expect(linkInvalid(refusal(404, "/api/v0/problems/not-found"))).toBe(false);
    expect(linkInvalid(refusal(422, "/api/v0/problems/validation-error"))).toBe(false);
  });

  // The actual status decides, so a failure that may have reset is not
  // taken for a spent link.
  it("ignores the type under another status", () => {
    expect(linkInvalid(refusal(500, linkInvalidType, linkInvalidDetail))).toBe(false);
    const misstated = new ApiError("http", {
      method: "POST",
      status: 500,
      problem: { type: linkInvalidType, status: 404 },
    });
    expect(linkInvalid(misstated)).toBe(false);
  });

  it("ignores a 404 without a problem", () => {
    expect(linkInvalid(new ApiError("http", { method: "POST", status: 404 }))).toBe(false);
  });
});

describe("completeErrorMessage", () => {
  it("keeps the server's text for a refusal", () => {
    const err = refusal(
      422,
      "/api/v0/problems/validation-error",
      "The request contains invalid values.",
    );

    expect(completeErrorMessage(err)).toBe("The request contains invalid values.");
  });

  // A reset deletes its link, so a second try reads "not valid".
  it("says how to tell whether an unknown try changed the password", () => {
    expect(completeErrorMessage(new ApiError("network", { method: "POST" }))).toBe(
      "We couldn't confirm whether your password was changed. Try again. If the link then no longer works, sign in with this new password.",
    );
  });
});

describe("endOf", () => {
  const spent = refusal(404, linkInvalidType, linkInvalidDetail);

  it("offers a new link for a link that no longer works", () => {
    expect(endOf(spent, "refused")).toEqual({
      message: linkInvalidDetail,
      links: ["new-link", "sign-in"],
    });
  });

  it("words a refusal without detail", () => {
    expect(endOf(refusal(404, linkInvalidType), "refused").message).toBe(
      "This link no longer works. Ask for a new one.",
    );
  });

  // A reset deletes its link, so the unknown try likely worked.
  it("points to sign-in after a try with an unknown outcome", () => {
    expect(endOf(spent, "unknown")).toEqual({
      message:
        "This link no longer works, so an earlier try probably changed your password. Sign in with the password you sent then. If you're not sure which one that was, ask for a new link.",
      links: ["sign-in", "new-link"],
    });
  });
});
