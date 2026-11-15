import { describe, expect, it } from "vite-plus/test";
import type { Member } from "@/api/generated/model";
import { ApiError } from "@/api/transport";
import { changedMessage, changeErrorMessage, formatDate, offeredAction } from "./text";

const httpStatus = { ok: 200, notFound: 404, conflict: 409, internalServerError: 500 };
const email = "member@example.com";

// member returns an active member the server lets an administrator change.
function member(changes: Partial<Member> = {}): Member {
  return {
    id: 7,
    email,
    role: "member",
    joinedAt: "2026-03-01T09:00:00Z",
    emailVerified: true,
    totpEnabled: false,
    deactivatedAt: null,
    allowedActions: ["deactivate", "reactivate"],
    ...changes,
  };
}

describe("member rows", () => {
  it("shows a date without its time", () => {
    // The exact wording follows the reader's locale, but never the time.
    expect(formatDate("2026-03-01T12:00:00Z")).not.toMatch(/\d:\d\d/);
  });

  it("offers the action that changes the status", () => {
    expect(offeredAction(member())).toBe("deactivate");
    expect(offeredAction(member({ deactivatedAt: "2026-11-02T09:30:00Z" }))).toBe("reactivate");
  });

  it("offers only what the server allows", () => {
    // The server decides who can be changed, e.g. not administrators.
    expect(offeredAction(member({ role: "admin", allowedActions: [] }))).toBeUndefined();
    expect(offeredAction(member({ allowedActions: ["reactivate"] }))).toBeUndefined();
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
      "Couldn't deactivate member@example.com. This account doesn't exist.",
    );
    const administrator = new ApiError("http", {
      status: httpStatus.conflict,
      method: "POST",
      problem: { detail: "Administrators can't be deactivated or reactivated." },
    });
    expect(changeErrorMessage(administrator, "reactivate", email)).toBe(
      "Couldn't reactivate member@example.com. Administrators can't be deactivated or reactivated.",
    );
  });

  it("says trying again is safe when the outcome is unknown", () => {
    const advice =
      "Couldn't confirm whether member@example.com was deactivated. Trying again is safe.";
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
