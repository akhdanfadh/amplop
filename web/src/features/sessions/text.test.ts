import { describe, expect, it } from "vite-plus/test";
import { ApiError } from "@/api/transport";
import { browserName, endedOthersMessage, endMessage, lastActive } from "./text";

const httpStatus = { ok: 200, notFound: 404, conflict: 409, internalServerError: 500 };
const sessionNotFound = {
  type: "/api/v0/problems/session-not-found",
  detail: "Session not found.",
};
const uncertain = "Could not confirm the sign-out. Check the list before trying again.";
const at = (time: string) => Date.parse(`2026-10-30T${time}Z`);

describe("session rows", () => {
  it("names the browser and operating system when known", () => {
    expect(browserName({ browser: "Firefox", os: "Linux" })).toBe("Firefox on Linux");
    expect(browserName({ browser: "Firefox", os: "" })).toBe("Firefox");
    expect(browserName({ browser: "", os: "Android" })).toBe("Unknown browser on Android");
    expect(browserName({ browser: " ", os: "" })).toBe("Unknown browser");
  });

  it("hides the exact time while activity may be unrecorded", () => {
    const lastActiveAt = "2026-10-30T09:00:00Z";

    expect(lastActive(lastActiveAt, at("09:00:00"))).toBe("Active in the last hour");
    expect(lastActive(lastActiveAt, at("09:59:59"))).toBe("Active in the last hour");
    // The exact wording follows the reader's locale.
    expect(lastActive(lastActiveAt, at("10:00:00"))).toMatch(/^Last active \S/);
  });
});

describe("session end messages", () => {
  it("reports a session the API says is gone", () => {
    const err = new ApiError("http", {
      status: httpStatus.notFound,
      method: "DELETE",
      problem: sessionNotFound,
    });
    expect(endMessage(err, "Firefox on Linux")).toBe("Firefox on Linux was already signed out.");
  });

  it("does not read a 404 from elsewhere as a sign-out", () => {
    const missingRoute = new ApiError("http", { status: httpStatus.notFound, method: "DELETE" });
    expect(endMessage(missingRoute, "Firefox on Linux")).not.toContain("signed out");
    const endAll = new ApiError("http", {
      status: httpStatus.notFound,
      method: "DELETE",
      problem: sessionNotFound,
    });
    expect(endMessage(endAll)).toBe("Session not found.");
  });

  it("asks for a look at the list when the outcome is unknown", () => {
    for (const err of [
      new ApiError("network", { method: "DELETE" }),
      new ApiError("timeout", { method: "DELETE" }),
      new ApiError("http", { status: httpStatus.internalServerError, method: "DELETE" }),
      new ApiError("response", { status: httpStatus.ok, method: "DELETE" }),
    ]) {
      expect(endMessage(err)).toBe(uncertain);
    }
    const explained = new ApiError("http", {
      status: httpStatus.internalServerError,
      method: "DELETE",
      problem: { detail: "Sessions are unavailable." },
    });
    expect(endMessage(explained)).toBe(`Sessions are unavailable. ${uncertain}`);
  });

  it("keeps a received rejection's explanation", () => {
    const err = new ApiError("http", {
      status: httpStatus.conflict,
      method: "DELETE",
      problem: { detail: "Sign out to end this browser's session." },
    });
    expect(endMessage(err, "Firefox on Linux")).toBe("Sign out to end this browser's session.");
  });

  it("counts the other browsers signed out", () => {
    expect(endedOthersMessage(0)).toBe("No other browsers were signed in.");
    expect(endedOthersMessage(1)).toBe("Signed out of 1 other browser.");
    expect(endedOthersMessage(3)).toBe("Signed out of 3 other browsers.");
  });
});
