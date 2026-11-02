import { describe, expect, it } from "vite-plus/test";
import { ApiError } from "@/api/transport";
import {
  confirmation,
  pendingMessage,
  saveErrorMessage,
  savedMessage,
  statusMessage,
  untestedMessage,
} from "./text";

describe("statusMessage", () => {
  it.each([
    ["closed", "Registration is closed. Visitors can't create accounts."],
    ["open", "Registration is open. Visitors can create accounts."],
    [
      "paused",
      "Registration is paused: the system email settings in use haven't passed a test, so visitors can't create accounts. It resumes once a test message passes.",
    ],
    ["unknown", "We couldn't confirm whether registration is open. Reload the page to check."],
  ] as const)("says what %s means", (status, message) => {
    expect(statusMessage(status)).toBe(message);
  });
});

describe("untestedMessage", () => {
  // Before the first save there is nothing to test yet.
  it("names both steps before opening", () => {
    expect(untestedMessage()).toBe(
      "To open registration, save the system email settings and send a test message that passes. Visitors get their sign-up link by email.",
    );
  });
});

describe("confirmation", () => {
  // US019 and AUTH.md: the administrator learns who can join and what it costs.
  it("says who can join before opening", () => {
    expect(confirmation("open")).toEqual({
      title: "Open registration?",
      description:
        "Anyone who can reach this inbok can create a member account. Each member adds storage and sync work to this server.",
      action: "Open registration",
    });
  });

  it("says what closing cancels and keeps", () => {
    expect(confirmation("close")).toEqual({
      title: "Close registration?",
      description:
        "Sign-up links that haven't been used stop working. Existing members keep their accounts.",
      action: "Close registration",
    });
  });
});

describe("pendingMessage", () => {
  it.each([
    ["open", "Opening registration..."],
    ["close", "Closing registration..."],
  ] as const)("says %s is saving", (change, message) => {
    expect(pendingMessage(change)).toBe(message);
  });
});

describe("savedMessage", () => {
  it.each([
    ["open", "Registration opened."],
    ["close", "Registration closed."],
  ] as const)("announces %s", (change, message) => {
    expect(savedMessage(change)).toBe(message);
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
      "Save the system email settings and send a test message that passes before opening registration.",
    );
  });

  it.each([
    ["open", "We couldn't confirm whether registration was opened. Try again."],
    ["close", "We couldn't confirm whether registration was closed. Try again."],
  ] as const)("offers another try when %s is uncertain", (change, message) => {
    expect(saveErrorMessage(new ApiError("network", { method: "PUT" }), change)).toBe(message);
  });

  it("keeps the server's explanation of an uncertain save", () => {
    const err = new ApiError("http", {
      method: "PUT",
      status: 500,
      problem: { detail: "Something failed." },
    });
    expect(saveErrorMessage(err, "close")).toBe(
      "Something failed. We couldn't confirm whether registration was closed. Try again.",
    );
  });
});
