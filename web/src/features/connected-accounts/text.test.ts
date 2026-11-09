import { describe, expect, it } from "vite-plus/test";
import { ConnectOutcome } from "@/api/generated/model";
import { ApiError } from "@/api/transport";
import {
  disconnectedMessage,
  disconnectErrorMessage,
  formatDate,
  outcomeNotice,
  startErrorMessage,
} from "./text";

const httpStatus = {
  ok: 200,
  notFound: 404,
  conflict: 409,
  tooManyRequests: 429,
  internalServerError: 500,
};
const email = "mail@example.com";

describe("connect outcomes", () => {
  it("words every outcome the contract lists", () => {
    for (const outcome of ConnectOutcome.options) {
      expect(outcomeNotice(outcome).text).toMatch(/\.$/);
    }
  });

  it("alerts only when the member has something to fix", () => {
    const kinds = Object.fromEntries(
      ConnectOutcome.options.map((outcome) => [outcome, outcomeNotice(outcome).kind]),
    );
    expect(kinds).toEqual({
      connected: "notice",
      cancelled: "notice",
      "gmail-not-granted": "alert",
      "email-not-verified": "alert",
      "no-gmail": "alert",
      "already-connected": "notice",
      "different-account": "alert",
      expired: "alert",
      failed: "alert",
    });
  });
});

describe("account rows", () => {
  it("shows a date without its time", () => {
    // The exact wording follows the reader's locale, but never the time.
    expect(formatDate("2026-03-01T12:00:00Z")).not.toMatch(/\d:\d\d/);
  });
});

describe("start messages", () => {
  it("keeps a received rejection's explanation", () => {
    const notConfigured = new ApiError("http", {
      status: httpStatus.conflict,
      method: "POST",
      problem: {
        detail: "Gmail can't be connected until an administrator sets up a Google client.",
      },
    });
    expect(startErrorMessage(notConfigured)).toBe(
      "Gmail can't be connected until an administrator sets up a Google client.",
    );
  });

  it("says when a throttled start can be tried again", () => {
    const throttled = new ApiError("http", {
      status: httpStatus.tooManyRequests,
      method: "POST",
      problem: { detail: "Too many Gmail connections were started recently." },
    });
    expect(startErrorMessage(throttled)).toMatch(
      /^Too many Gmail connections were started recently\. Try again /,
    );
  });

  it("reports an account the API says is gone", () => {
    const gone = new ApiError("http", {
      status: httpStatus.notFound,
      method: "POST",
      problem: {
        type: "/api/v0/problems/connected-account-not-found",
        detail: "The connected account does not exist.",
      },
    });
    expect(startErrorMessage(gone)).toBe(
      "That account is no longer connected, so it can't be reconnected.",
    );
  });

  it("keeps a server's explanation before the advice", () => {
    const explained = new ApiError("http", {
      status: httpStatus.internalServerError,
      method: "POST",
      problem: { detail: "The database is unavailable." },
    });
    expect(startErrorMessage(explained)).toBe(
      "The database is unavailable. Could not open Google's page. Try again.",
    );
  });

  it("invites another try when the outcome is unknown", () => {
    // Nothing needs checking: an unused start only expires.
    for (const err of [
      new ApiError("network", { method: "POST" }),
      new ApiError("http", { status: httpStatus.internalServerError, method: "POST" }),
      new ApiError("response", { status: httpStatus.ok, method: "POST" }),
    ]) {
      expect(startErrorMessage(err)).toBe("Could not open Google's page. Try again.");
    }
  });
});

describe("disconnect messages", () => {
  it("reports a confirmed disconnect by the account's address", () => {
    expect(disconnectedMessage(email)).toBe("mail@example.com is disconnected.");
  });

  it("reports an account the API says is gone", () => {
    const gone = new ApiError("http", {
      status: httpStatus.notFound,
      method: "DELETE",
      problem: {
        type: "/api/v0/problems/connected-account-not-found",
        detail: "The connected account does not exist.",
      },
    });
    expect(disconnectErrorMessage(gone, email)).toBe("mail@example.com was already disconnected.");
  });

  it("keeps a 404 without the API's type as a failure", () => {
    const missingRoute = new ApiError("http", { status: httpStatus.notFound, method: "DELETE" });
    expect(disconnectErrorMessage(missingRoute, email)).toMatch(
      /^Could not disconnect mail@example\.com\. /,
    );
  });

  it("keeps a received rejection's explanation and names the account", () => {
    const refused = new ApiError("http", {
      status: httpStatus.conflict,
      method: "DELETE",
      problem: { detail: "The account is busy." },
    });
    expect(disconnectErrorMessage(refused, email)).toBe(
      "Could not disconnect mail@example.com. The account is busy.",
    );
  });

  it("asks for a look at the list when the outcome is unknown", () => {
    const advice =
      "Could not confirm whether mail@example.com is disconnected. Check the list before trying again.";
    for (const err of [
      new ApiError("network", { method: "DELETE" }),
      new ApiError("timeout", { method: "DELETE" }),
      new ApiError("http", { status: httpStatus.internalServerError, method: "DELETE" }),
    ]) {
      expect(disconnectErrorMessage(err, email)).toBe(advice);
    }
    const explained = new ApiError("http", {
      status: httpStatus.internalServerError,
      method: "DELETE",
      problem: { detail: "The database is unavailable." },
    });
    expect(disconnectErrorMessage(explained, email)).toBe(`The database is unavailable. ${advice}`);
  });

  it("asks for a look at the list when the result is unreadable", () => {
    const unreadable = new ApiError("response", { status: httpStatus.ok, method: "DELETE" });
    expect(disconnectErrorMessage(unreadable, email)).toBe(
      "Could not confirm whether mail@example.com is disconnected. Check the list before trying again.",
    );
  });
});
