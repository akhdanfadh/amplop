import { describe, expect, it } from "vite-plus/test";
import type { RegistrationSettings } from "@/api/generated/model";
import { ApiError } from "@/api/transport";
import { actionWords, offeredAction, saveErrorMessage, statusMessage } from "./text";

describe("statusMessage", () => {
  it.each([
    ["closed", "Closed. Visitors can't create accounts."],
    ["open", "Open. Anyone who can reach this amplop can create a member account."],
  ] as const)("says what %s means for visitors", (status, message) => {
    expect(statusMessage(status)).toBe(message);
  });

  it("says why paused registration takes no one and how it resumes", () => {
    expect(statusMessage("paused")).toBe(
      "Paused. Registration is open, but visitors can't create accounts until a test message passes with the system email settings in use.",
    );
  });
});

describe("offeredAction", () => {
  const tested: RegistrationSettings["allowedActions"] = ["open", "close"];
  it.each([
    ["closed", tested, "open"],
    ["open", tested, "close"],
    ["paused", ["close"], "close"],
  ] as const)("offers the change from %s", (status, allowedActions, want) => {
    expect(
      offeredAction({ status, systemEmailTested: true, allowedActions: [...allowedActions] }),
    ).toBe(want);
  });

  it("offers nothing the server refuses", () => {
    // Opening waits for tested system email; the server says so, not the page.
    expect(
      offeredAction({ status: "closed", systemEmailTested: false, allowedActions: ["close"] }),
    ).toBeUndefined();
  });
});

describe("actionWords", () => {
  it("says who can join before opening", () => {
    expect(actionWords("open").description).toMatch(/Anyone who can reach this amplop/);
  });

  it("says what closing cancels and keeps", () => {
    expect(actionWords("close").description).toBe(
      "Sign-up links that haven't been used stop working. Existing members keep their accounts.",
    );
  });
});

describe("saveErrorMessage", () => {
  it("keeps the server's text for a refusal", () => {
    const err = new ApiError("http", {
      method: "PUT",
      status: 409,
      problem: {
        type: "/api/v0/problems/system-email-not-tested",
        detail:
          "Save the system email settings and send a test message that passes before opening registration.",
      },
    });
    expect(saveErrorMessage(err, "open")).toBe(
      "Couldn't open registration. Save the system email settings and send a test message that passes before opening registration.",
    );
  });

  it.each([
    ["open", "Couldn't confirm whether registration was opened. Trying again is safe."],
    ["close", "Couldn't confirm whether registration was closed. Trying again is safe."],
  ] as const)("offers another try when %s is uncertain", (action, message) => {
    expect(saveErrorMessage(new ApiError("network", { method: "PUT" }), action)).toBe(message);
  });

  it("keeps the server's explanation of an uncertain save", () => {
    const err = new ApiError("http", {
      method: "PUT",
      status: 500,
      problem: { detail: "Something failed." },
    });
    expect(saveErrorMessage(err, "close")).toBe(
      "Something failed. Couldn't confirm whether registration was closed. Trying again is safe.",
    );
  });
});
