import { describe, expect, it } from "vite-plus/test";
import { ConnectOutcome } from "@/api/generated/model";
import { ApiError } from "@/api/transport";
import {
  disconnectedMessage,
  disconnectErrorMessage,
  failureWords,
  formatDate,
  formatMoment,
  outcomeNotice,
  runningText,
  startErrorMessage,
  syncErrorMessage,
  syncStartedMessage,
} from "./text";

const httpStatus = {
  ok: 200,
  notFound: 404,
  conflict: 409,
  tooManyRequests: 429,
  internalServerError: 500,
};
const email = "mail@example.com";

// refusal returns the server's refusal with status and problem type.
function refusal(status: number, type: string): ApiError {
  return new ApiError("http", {
    method: "POST",
    status,
    problem: { type, status, title: "Refused", detail: "Refused." },
  });
}

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

describe("wording", () => {
  it("counts the messages copied", () => {
    expect(runningText(1)).toBe(
      "Copying your last 90 days of mail into inbok: 1 message so far. New mail shows up in the meantime.",
    );
    expect(runningText(0)).toMatch(/: 0 messages so far\./);
    // A comma for thousands whatever the reader's locale.
    expect(runningText(2140)).toMatch(/: 2,140 messages so far\./);
  });

  it("words a failure by its cause and what happens next", () => {
    expect(failureWords("provider", "complete")).toEqual({
      before: "Couldn't get mail from Gmail at",
      after: ". inbok will try again.",
    });
    expect(failureWords("provider", "failed").after).toBe(". inbok will try again within an hour.");
    for (const failure of ["token-unreadable", "internal"] as const)
      expect(failureWords(failure, "failed")).toEqual({
        before: "Couldn't get mail at",
        after: " because of a problem on inbok's side. Ask your administrator.",
      });
  });

  it("shows a time with its date only on another day", () => {
    const now = new Date(2026, 10, 11, 12, 0);
    const today = new Date(2026, 10, 11, 9, 30).toISOString();
    const yesterday = new Date(2026, 10, 10, 9, 30).toISOString();
    expect(formatMoment(today, now)).not.toMatch(/2026/);
    expect(formatMoment(yesterday, now)).toMatch(/2026/);
  });

  it("says a Check now started", () => {
    expect(syncStartedMessage(email)).toBe(`Checking ${email} for new mail.`);
  });
});

describe("syncErrorMessage", () => {
  it("words each refusal by its type", () => {
    expect(
      syncErrorMessage(refusal(404, "/api/v0/problems/connected-account-not-found"), email),
    ).toBe(`${email} is no longer connected.`);
    expect(syncErrorMessage(refusal(409, "/api/v0/problems/account-needs-reconnect"), email)).toBe(
      `Reconnect ${email} before checking it for mail.`,
    );
    expect(syncErrorMessage(refusal(429, "/api/v0/problems/sync-throttled"), email)).toBe(
      `inbok started checking ${email} moments ago. See its status below.`,
    );
  });

  it("asks for a look when the outcome is unknown", () => {
    const lost = new ApiError("network", { method: "POST" });
    expect(syncErrorMessage(lost, email)).toBe(
      `Couldn't tell whether inbok started checking ${email}. See its status below.`,
    );
  });
});
