import { describe, expect, it } from "vite-plus/test";
import { ConnectOutcome } from "@/api/generated/model";
import { ApiError } from "@/api/transport";
import {
  alreadyDisconnectedNotice,
  disconnectedNotice,
  disconnectFailure,
  disconnectWords,
  failureWords,
  formatDate,
  formatMoment,
  isAccountGone,
  outcomeNotice,
  runningText,
  startErrorMessage,
  syncNotice,
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

// throttledAfter returns Check now's 429 with the Retry-After header wait.
function throttledAfter(wait: string | undefined): ApiError {
  return new ApiError("http", {
    method: "POST",
    status: httpStatus.tooManyRequests,
    retryAfter: wait,
    receivedAt: Date.now(),
    problem: {
      type: "/api/v0/problems/sync-throttled",
      status: httpStatus.tooManyRequests,
      title: "Sync requested recently",
      detail: "Requested moments ago.",
    },
  });
}

describe("connect outcomes", () => {
  it("words every outcome the contract lists", () => {
    for (const outcome of ConnectOutcome.options) {
      expect(outcomeNotice(outcome).text).toMatch(/\.$/);
    }
  });

  it("is a problem only when the member has something to fix", () => {
    const tones = Object.fromEntries(
      ConnectOutcome.options.map((outcome) => [outcome, outcomeNotice(outcome).tone]),
    );
    expect(tones).toEqual({
      connected: "success",
      cancelled: "neutral",
      "gmail-not-granted": "problem",
      "email-not-verified": "problem",
      "no-gmail": "problem",
      "already-connected": "neutral",
      "different-account": "problem",
      expired: "problem",
      failed: "problem",
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
  it("names the account in the dialog's button", () => {
    const words = disconnectWords("Work", false);
    expect(words.action).toBe("Disconnect Work");
    expect(words.description).toMatch(/asks Google to end its access\. Your mail in Gmail/);
    expect(disconnectWords("Work", true).description).toMatch(/can't ask Google to end its access/);
  });

  it("reports a confirmed disconnect, and one already done, by the account's name", () => {
    expect(disconnectedNotice("Work")).toEqual({ tone: "success", text: "Work is disconnected." });
    expect(alreadyDisconnectedNotice("Work")).toEqual({
      tone: "neutral",
      text: "Work was already disconnected.",
    });
  });

  it("takes only the API's own 404 as an account already gone", () => {
    const gone = new ApiError("http", {
      status: httpStatus.notFound,
      method: "DELETE",
      problem: {
        type: "/api/v0/problems/connected-account-not-found",
        detail: "The connected account does not exist.",
      },
    });
    expect(isAccountGone(gone)).toBe(true);
    const missingRoute = new ApiError("http", { status: httpStatus.notFound, method: "DELETE" });
    expect(isAccountGone(missingRoute)).toBe(false);
    expect(disconnectFailure(missingRoute, email)).toMatch(
      /^Couldn't disconnect mail@example\.com\. /,
    );
  });

  it("keeps a received rejection's explanation and names the account", () => {
    const refused = new ApiError("http", {
      status: httpStatus.conflict,
      method: "DELETE",
      problem: { detail: "The account is busy." },
    });
    expect(disconnectFailure(refused, email)).toBe(
      "Couldn't disconnect mail@example.com. The account is busy.",
    );
  });

  it("asks for a look at the list when the outcome is unknown", () => {
    const advice =
      "Couldn't confirm whether mail@example.com is disconnected. Check the list before trying again.";
    for (const err of [
      new ApiError("network", { method: "DELETE" }),
      new ApiError("timeout", { method: "DELETE" }),
      new ApiError("http", { status: httpStatus.internalServerError, method: "DELETE" }),
      new ApiError("response", { status: httpStatus.ok, method: "DELETE" }),
    ]) {
      expect(disconnectFailure(err, email)).toBe(advice);
    }
    const explained = new ApiError("http", {
      status: httpStatus.internalServerError,
      method: "DELETE",
      problem: { detail: "The database is unavailable." },
    });
    expect(disconnectFailure(explained, email)).toBe(`The database is unavailable. ${advice}`);
  });
});

describe("wording", () => {
  it("counts the messages copied", () => {
    expect(runningText(1)).toBe(
      "Copying your last 90 days of mail into amplop: 1 message so far. New mail shows up in the meantime.",
    );
    expect(runningText(0)).toMatch(/: 0 messages so far\./);
    // A comma for thousands whatever the reader's locale.
    expect(runningText(2140)).toMatch(/: 2,140 messages so far\./);
  });

  it("words a failure by its cause and what happens next", () => {
    expect(failureWords("provider", "complete")).toEqual({
      before: "Couldn't get mail from Gmail at",
      after: ". amplop will try again.",
    });
    expect(failureWords("provider", "failed").after).toBe(
      ". amplop will try again within an hour.",
    );
    for (const failure of ["token-unreadable", "internal"] as const)
      expect(failureWords(failure, "failed")).toEqual({
        before: "Couldn't get mail at",
        after: " because of a problem on amplop's side. Ask your administrator.",
      });
  });

  it("shows a time with its date only on another day", () => {
    const now = new Date(2026, 10, 11, 12, 0);
    const today = new Date(2026, 10, 11, 9, 30).toISOString();
    const yesterday = new Date(2026, 10, 10, 9, 30).toISOString();
    expect(formatMoment(today, now)).not.toMatch(/2026/);
    expect(formatMoment(yesterday, now)).toMatch(/2026/);
  });
});

describe("syncNotice", () => {
  it("words each refusal by its type", () => {
    expect(syncNotice(refusal(404, "/api/v0/problems/connected-account-not-found"), email)).toEqual(
      { tone: "neutral", text: `${email} is no longer connected.` },
    );
    expect(syncNotice(refusal(409, "/api/v0/problems/account-needs-reconnect"), email)).toEqual({
      tone: "problem",
      text: `Reconnect ${email} before checking it for mail.`,
    });
  });

  it("words a wait under a minute, not as a clock time", () => {
    // The limit is 15 seconds, which a time rounded up to the minute would
    // overstate.
    for (const wait of ["12", "60"])
      expect(syncNotice(throttledAfter(wait), email)).toEqual({
        tone: "neutral",
        text: `${email} was checked moments ago. Check again in a few seconds.`,
      });
  });

  it("gives a longer or unreadable wait as the usual time to try again", () => {
    for (const wait of ["61", undefined, "soon"]) {
      const notice = syncNotice(throttledAfter(wait), email);
      expect(notice.tone).toBe("neutral");
      expect(notice.text).not.toMatch(/a few seconds/);
      expect(notice.text).toMatch(/^mail@example\.com: Requested moments ago\. Try again/);
    }
  });

  it("says the mail still comes when the outcome is unknown", () => {
    const lost = new ApiError("network", { method: "POST" });
    expect(syncNotice(lost, email)).toEqual({
      tone: "neutral",
      text: `Couldn't tell whether amplop started checking ${email}. Its mail still arrives within a minute.`,
    });
  });
});
