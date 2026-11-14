import { describe, expect, it } from "vite-plus/test";
import type { OutgoingMessageSummary, ProblemType } from "@/api/generated/model";
import { ApiError } from "@/api/transport";
import {
  fromOption,
  inProgress,
  keyReused,
  leaveText,
  mayHaveSaved,
  needsReconnect,
  nextSteps,
  statusLabel,
  readErrorMessage,
  recipientSummary,
  replyErrorMessage,
  sendAgainErrorMessage,
  sendErrorMessage,
  unfinished,
  type MessageState,
  type Offer,
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

describe("nextSteps", () => {
  const copy = "0b9f3a52-5f1d-4a8e-9a43-5f0c2b6b1c11";

  it("says the account to check for an unknown outcome", () => {
    expect(nextSteps({ ...waiting, status: "unknown" }).text).toBe(
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
    const { text } = nextSteps(failed(failure));
    expect(text.startsWith("Not sent. ")).toBe(true);
    expect(text).toContain(words);
  });

  it("names the address Send again uses after a sender change, when known", () => {
    expect(nextSteps(failed("sender-changed"), "new@example.com").text).toContain(
      "sends it from new@example.com.",
    );
    expect(nextSteps(failed("sender-changed")).text).toContain("sends it from the new address.");
  });

  it.each<[string, MessageState, Offer[]]>([
    ["waiting", waiting, []],
    ["sending", { ...waiting, status: "sending" }, []],
    ["sent", { ...waiting, status: "sent" }, []],
    ["unknown", { ...waiting, status: "unknown" }, ["send-again"]],
    ["refused", failed("refused"), ["edit"]],
    ["sending-limit", failed("sending-limit"), ["send-again"]],
    ["needs-reconnect", failed("needs-reconnect"), ["send-again", "reconnect"]],
    ["token-unreadable", failed("token-unreadable"), ["send-again", "reconnect"]],
    ["deactivated", failed("deactivated"), ["send-again"]],
    ["sender-changed", failed("sender-changed"), ["send-again"]],
    ["gave-up", failed("gave-up"), ["send-again"]],
  ])("offers what a %s message allows", (_, message, offers) => {
    expect(nextSteps(message).offers).toEqual(offers);
  });

  // Each reason's own text says to send it again or reconnect; once a copy
  // exists, following it would send the message twice.
  it.each<[string, MessageState, string]>([
    [
      "failed",
      { ...failed("needs-reconnect"), sentAgainAs: copy },
      "Not sent. You already tried sending it again.",
    ],
    [
      "unknown",
      { ...waiting, status: "unknown", sentAgainAs: copy },
      "inbok couldn't confirm whether Gmail sent this message. You already tried sending it again.",
    ],
  ])("offers nothing more for a %s message already sent again", (_, message, text) => {
    expect(nextSteps(message)).toEqual({ text, offers: [] });
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

describe("sendErrorMessage", () => {
  it("asks for a look at the Outbox when the key saved another message", () => {
    expect(sendErrorMessage(problem(422, "/api/v0/problems/idempotency-key-reused"))).toBe(
      "An earlier try may have saved this message before your last edits. Check the Outbox before sending again.",
    );
  });

  it("says sending again is safe after a lost answer", () => {
    expect(sendErrorMessage(new ApiError("network", { method: "POST" }))).toBe(
      "We couldn't confirm whether your message was saved. Sending again is safe: it won't be sent twice.",
    );
  });

  it("keeps the server's explanation of a refusal", () => {
    expect(sendErrorMessage(problem(409, "/api/v0/problems/sending-account-needs-reconnect"))).toBe(
      "The server's detail.",
    );
  });
});

describe("replyErrorMessage", () => {
  it("says the answered message was deleted on its own 404 only", () => {
    expect(replyErrorMessage(problem(404, "/api/v0/problems/message-not-found"))).toBe(
      "The message you're replying to was deleted, so this reply can't be sent. Copy what you typed to keep it.",
    );
    expect(replyErrorMessage(problem(404, "/api/v0/problems/not-found"))).toBe(
      "The server's detail.",
    );
  });

  it("asks for a look at the Outbox when the key saved another reply", () => {
    expect(replyErrorMessage(problem(422, "/api/v0/problems/idempotency-key-reused"))).toContain(
      "this reply before your last edits",
    );
  });

  it("says sending again is safe after a lost answer", () => {
    expect(replyErrorMessage(new ApiError("network", { method: "POST" }))).toBe(
      "We couldn't confirm whether your reply was saved. Sending again is safe: it won't be sent twice.",
    );
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
  it("names each status", () => {
    expect(statusLabel("failed")).toBe("Not sent");
    expect(statusLabel("unknown")).toBe("Not confirmed");
  });

  it("follows only a send still in progress", () => {
    expect(inProgress("waiting")).toBe(true);
    expect(inProgress("sending")).toBe(true);
    expect(inProgress("unknown")).toBe(false);
    expect(inProgress("sent")).toBe(false);
  });

  it("counts a message unfinished until it is sent, or sent again", () => {
    const copy = "0b9f3a52-5f1d-4a8e-9a43-5f0c2b6b1c11";
    expect(unfinished(waiting)).toBe(true);
    expect(unfinished({ ...waiting, status: "sending" })).toBe(true);
    expect(unfinished(failed("gave-up"))).toBe(true);
    expect(unfinished({ ...waiting, status: "unknown" })).toBe(true);
    expect(unfinished({ ...waiting, status: "sent", sentAt: "2026-11-13T08:00:05Z" })).toBe(false);
    // Its copy carries on; the copy's own status counts instead.
    expect(unfinished({ ...failed("gave-up"), sentAgainAs: copy })).toBe(false);
    expect(unfinished({ ...waiting, status: "unknown", sentAgainAs: copy })).toBe(false);
  });

  it("tells problems apart by status and type together", () => {
    const reconnect = "/api/v0/problems/sending-account-needs-reconnect";
    expect(needsReconnect(problem(409, reconnect))).toBe(true);
    expect(needsReconnect(problem(422, reconnect))).toBe(false);
    expect(needsReconnect(null)).toBe(false);
    expect(keyReused(problem(422, "/api/v0/problems/idempotency-key-reused"))).toBe(true);
    expect(keyReused(problem(409, "/api/v0/problems/idempotency-key-reused"))).toBe(false);
  });
});

describe("leaveText", () => {
  it("asks to stay while a send is saving", () => {
    expect(leaveText("sending").title).toBe("Leave while sending?");
  });

  // Its error sends the member to the Outbox, so the question must not
  // assume they leave without checking: it fits any destination.
  it("warns that an earlier try may have saved the message, wherever the member goes", () => {
    expect(leaveText("unconfirmed")).toEqual({
      title: "Leave this page?",
      description:
        "An earlier try may have saved this message, and it may be sent. What you typed here won't be kept.",
      stay: "Stay",
      leave: "Leave",
      leaveVariant: "default",
    });
  });

  it("says typed input is lost when nothing was saved", () => {
    expect(leaveText("typed")).toMatchObject({ leave: "Discard", leaveVariant: "destructive" });
  });
});

describe("mayHaveSaved", () => {
  it("is true for a lost answer and a key an earlier try used", () => {
    expect(mayHaveSaved(new ApiError("network", { method: "POST" }))).toBe(true);
    expect(mayHaveSaved(problem(422, "/api/v0/problems/idempotency-key-reused"))).toBe(true);
  });

  it("is false for a refusal that saved nothing", () => {
    expect(mayHaveSaved(problem(422, "/api/v0/problems/validation-error"))).toBe(false);
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
    expect(sendErrorMessage(err)).toMatch(/^Too many messages\. Try again after /);
    expect(sendAgainErrorMessage(err)).toMatch(/^Too many messages\. Try again after /);
  });
});

describe("readErrorMessage for other failures", () => {
  it("keeps the general explanation", () => {
    const err = new ApiError("http", { method: "GET", status: 503 });
    expect(readErrorMessage(err)).toMatch(/^Could not load this message\. /);
  });
});

describe("fromOption", () => {
  it("names an account by its address, then the name only the member sees", () => {
    expect(fromOption({ email: "ana@work.example", name: null })).toBe("ana@work.example");
    expect(fromOption({ email: "ana@work.example", name: "Work" })).toBe("ana@work.example (Work)");
  });
});
