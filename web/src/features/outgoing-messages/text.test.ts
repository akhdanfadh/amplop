import { describe, expect, it } from "vite-plus/test";
import type { OutgoingMessageSummary, ProblemType } from "@/api/generated/model";
import { ApiError } from "@/api/transport";
import {
  inProgress,
  needsReconnect,
  reconnectAdvised,
  statusLabel,
  statusVariant,
  readErrorMessage,
  recipientSummary,
  sendAgainErrorMessage,
  sendableAgain,
  statusText,
  type MessageState,
} from "./text";

type Failure = NonNullable<OutgoingMessageSummary["failure"]>;

const from = "me@example.com";
const waiting: MessageState = {
  status: "waiting",
  failure: null,
  from,
  sentAt: null,
  sentAgainAs: null,
};

// failed returns a message that failed for failure.
function failed(failure: Failure): MessageState {
  return { ...waiting, status: "failed", failure };
}

// problem returns a received error answer of status with the problem type.
function problem(status: number, type: ProblemType): ApiError {
  return new ApiError("http", {
    method: "POST",
    status,
    problem: { type, status, title: "Refused", detail: "The server's detail." },
  });
}

describe("statusText", () => {
  it("says the account to check for an unknown outcome", () => {
    expect(statusText({ ...waiting, status: "unknown" })).toBe(
      "inbok couldn't confirm whether Gmail sent this message. Check the Sent folder of me@example.com in Gmail before sending it again.",
    );
  });

  it.each<[Failure, string]>([
    ["refused", "Gmail refused this message. Check its recipients and content"],
    [
      "sending-limit",
      "me@example.com reached Gmail's daily sending limit, which lifts within 24 hours.",
    ],
    ["needs-reconnect", "Reconnect the account on the Connected accounts page"],
    ["token-unreadable", "Reconnect the account on the Connected accounts page"],
    ["deactivated", "Your inbok account was deactivated"],
    ["gave-up", "Google didn't take this message after several tries"],
  ])("words %s", (failure, words) => {
    const text = statusText(failed(failure));
    expect(text.startsWith("Not sent. ")).toBe(true);
    expect(text).toContain(words);
  });

  it("names the address Send again uses after a sender change, when known", () => {
    expect(statusText(failed("sender-changed"), "new@example.com")).toContain(
      "sends it from new@example.com.",
    );
    expect(statusText(failed("sender-changed"))).toContain("sends it from the new address.");
  });
});

describe("sendableAgain", () => {
  it("offers Send again for unknown and failed messages, except refused ones", () => {
    expect(sendableAgain({ ...waiting, status: "unknown" })).toBe(true);
    expect(sendableAgain(failed("gave-up"))).toBe(true);
    expect(sendableAgain(failed("refused"))).toBe(false);
    expect(sendableAgain({ ...waiting, status: "sent" })).toBe(false);
    expect(sendableAgain(waiting)).toBe(false);
  });

  it("offers nothing more for a message already sent again", () => {
    const copy = "0b9f3a52-5f1d-4a8e-9a43-5f0c2b6b1c11";
    expect(sendableAgain({ ...failed("gave-up"), sentAgainAs: copy })).toBe(false);
  });
});

describe("recipientSummary", () => {
  it.each([
    [{ to: [], cc: [], bcc: [] }, "No recipients"],
    [{ to: ["ana@example.com"], cc: [], bcc: [] }, "ana@example.com"],
    [{ to: [], cc: ["ana@example.com"], bcc: ["bo@example.com"] }, "ana@example.com and 1 other"],
    [{ to: ["a@x.io", "b@x.io"], cc: [], bcc: ["c@x.io"] }, "a@x.io and 2 others"],
  ])("summarizes %j", (lists, summary) => {
    expect(recipientSummary(lists)).toBe(summary);
  });
});

describe("sendAgainErrorMessage", () => {
  it("says the status changed on 409 not-sendable-again", () => {
    expect(sendAgainErrorMessage(problem(409, "/api/v0/problems/not-sendable-again"))).toBe(
      "This message can't be sent again: its status has changed.",
    );
  });

  it("says the message is gone on its own 404 only", () => {
    expect(
      sendAgainErrorMessage(problem(404, "/api/v0/problems/outgoing-message-not-found")),
    ).toContain("isn't in your Outbox");
    expect(sendAgainErrorMessage(problem(404, "/api/v0/problems/not-found"))).toBe(
      "The server's detail.",
    );
  });

  it("says trying again is safe after a lost answer", () => {
    expect(sendAgainErrorMessage(new ApiError("timeout", { method: "POST" }))).toContain(
      "Trying again is safe",
    );
  });
});

describe("readErrorMessage", () => {
  it("says a missing message is gone", () => {
    const err = new ApiError("http", {
      method: "GET",
      status: 404,
      problem: { type: "/api/v0/problems/outgoing-message-not-found", status: 404 },
    });
    expect(readErrorMessage(err)).toContain("isn't in your Outbox");
  });
});

describe("status helpers", () => {
  it("names each status and marks the ones that ask something", () => {
    expect(statusLabel("failed")).toBe("Not sent");
    expect(statusLabel("unknown")).toBe("Not confirmed");
    expect(statusVariant("failed")).toBe("destructive");
    expect(statusVariant("unknown")).toBe("destructive");
    expect(statusVariant("sending")).toBe("secondary");
  });

  it("follows only a send still in progress", () => {
    expect(inProgress("waiting")).toBe(true);
    expect(inProgress("sending")).toBe(true);
    expect(inProgress("unknown")).toBe(false);
    expect(inProgress("sent")).toBe(false);
  });

  it("advises reconnecting for the two failures a reconnect fixes", () => {
    expect(reconnectAdvised({ failure: "needs-reconnect" })).toBe(true);
    expect(reconnectAdvised({ failure: "token-unreadable" })).toBe(true);
    expect(reconnectAdvised({ failure: "gave-up" })).toBe(false);
  });

  it("tells problems apart by status and type together", () => {
    const reconnect = "/api/v0/problems/sending-account-needs-reconnect";
    expect(needsReconnect(problem(409, reconnect))).toBe(true);
    expect(needsReconnect(problem(422, reconnect))).toBe(false);
    expect(needsReconnect(null)).toBe(false);
  });
});

describe("errors with a wait", () => {
  it("says when to send again after the send limit", () => {
    const err = new ApiError("http", {
      method: "POST",
      status: 429,
      retryAfter: "60",
      receivedAt: Date.UTC(2026, 10, 10, 10, 0, 0),
      problem: { type: "/api/v0/problems/send-throttled", status: 429, title: "Too many messages" },
    });
    expect(sendAgainErrorMessage(err)).toMatch(/^Too many messages\. Try again after /);
  });
});

describe("readErrorMessage for other failures", () => {
  it("keeps the general explanation", () => {
    const err = new ApiError("http", { method: "GET", status: 503 });
    expect(readErrorMessage(err)).toMatch(/^Could not load this message\. /);
  });
});
