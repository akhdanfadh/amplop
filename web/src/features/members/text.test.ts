import { describe, expect, it } from "vite-plus/test";
import { ApiError } from "@/api/transport";
import { canChange, changedMessage, changeErrorMessage, formatDate, roleName } from "./text";

const httpStatus = { ok: 200, notFound: 404, conflict: 409, internalServerError: 500 };
const email = "member@example.com";

describe("member rows", () => {
  it("names each role", () => {
    expect(roleName("admin")).toBe("Administrator");
    expect(roleName("member")).toBe("Member");
  });

  it("shows a date without its time", () => {
    // The exact wording follows the reader's locale, but never the time.
    expect(formatDate("2026-03-01T12:00:00Z")).not.toMatch(/\d:\d\d/);
  });

  it("offers changes for other members only", () => {
    const you = "admin@example.com";
    expect(canChange({ role: "member", email }, you)).toBe(true);
    expect(canChange({ role: "admin", email: "other-admin@example.com" }, you)).toBe(false);
    expect(canChange({ role: "admin", email: you }, you)).toBe(false);
    // A member is never signed in on this page, but their own row still has no action.
    expect(canChange({ role: "member", email: you }, you)).toBe(false);
  });
});

describe("member change messages", () => {
  it("reports a confirmed change by the member's address", () => {
    expect(changedMessage("deactivate", email)).toBe("member@example.com is deactivated.");
    expect(changedMessage("reactivate", email)).toBe("member@example.com is reactivated.");
  });

  it("keeps a received rejection's explanation and names the member", () => {
    const missing = new ApiError("http", {
      status: httpStatus.notFound,
      method: "POST",
      problem: { detail: "This account doesn't exist." },
    });
    expect(changeErrorMessage(missing, "deactivate", email)).toBe(
      "Could not deactivate member@example.com. This account doesn't exist.",
    );
    const administrator = new ApiError("http", {
      status: httpStatus.conflict,
      method: "POST",
      problem: { detail: "Administrators can't be deactivated or reactivated." },
    });
    expect(changeErrorMessage(administrator, "reactivate", email)).toBe(
      "Could not reactivate member@example.com. Administrators can't be deactivated or reactivated.",
    );
  });

  it("asks for a look at the list when the outcome is unknown", () => {
    const advice =
      "Could not confirm whether member@example.com is deactivated. Check the list before trying again.";
    for (const err of [
      new ApiError("network", { method: "POST" }),
      new ApiError("timeout", { method: "POST" }),
      new ApiError("http", { status: httpStatus.internalServerError, method: "POST" }),
      new ApiError("response", { status: httpStatus.ok, method: "POST" }),
    ]) {
      expect(changeErrorMessage(err, "deactivate", email)).toBe(advice);
    }
    const explained = new ApiError("http", {
      status: httpStatus.internalServerError,
      method: "POST",
      problem: { detail: "The database is unavailable." },
    });
    expect(changeErrorMessage(explained, "deactivate", email)).toBe(
      `The database is unavailable. ${advice}`,
    );
  });
});
