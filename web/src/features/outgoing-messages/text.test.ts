import { describe, expect, it } from "vite-plus/test";
import type { OutgoingMessageSummary, ProblemType } from "@/api/generated/model";
import { ApiError } from "@/api/transport";
import {
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
  type SendingAccount,
  invalidAddressMessage,
  noAccountText,
  cannotSendNote,
  rowReason,
} from "./text";
import { messageLook } from "./status-badge";

type Failure = NonNullable<OutgoingMessageSummary["failure"]>;

const from = "me@example.com";
const waiting: MessageState = {
  status: "waiting",
  failure: null,
  from,
  sentAt: null,
  sentAgainAs: null,
  allowedActions: [],
  connectedAccountId: 1,
};

// failed returns a message that failed for failure, which the API allows
// to be sent again.
function failed(failure: Failure): MessageState {
  return { ...waiting, status: "failed", failure, allowedActions: ["send-again"] };
}

// The sending account, usable, and another of the member's accounts.
const own: SendingAccount = { id: 1, email: from, status: "usable", allowedActions: ["send"] };
const other: SendingAccount = { ...own, id: 2, email: "other@example.com" };
// The sending account once Google stopped accepting its access.
const stale: SendingAccount = { ...own, status: "needs-reconnect", allowedActions: [] };

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
    expect(
      nextSteps(failed("sender-changed"), [{ ...own, email: "new@example.com" }]).text,
    ).toContain("sends it from new@example.com.");
    expect(nextSteps(failed("sender-changed")).text).toContain("sends it from the new address.");
  });

  const unknown: MessageState = { ...waiting, status: "unknown", allowedActions: ["send-again"] };

  it.each<[string, MessageState, SendingAccount[] | undefined, Offer[]]>([
    ["waiting", waiting, [own], []],
    ["sending", { ...waiting, status: "sending" }, [own], []],
    ["sent", { ...waiting, status: "sent" }, [own], []],
    ["unknown", unknown, [own], ["send-again"]],
    ["sending-limit", failed("sending-limit"), [own], ["send-again"]],
    ["deactivated", failed("deactivated"), [own], ["send-again"]],
    ["sender-changed", failed("sender-changed"), [own], ["send-again"]],
    ["gave-up", failed("gave-up"), [own], ["send-again"]],
    // The API decides Send again, e.g. not while Gmail isn't set up.
    ["gave-up, not allowed", { ...failed("gave-up"), allowedActions: [] }, [own], []],
    ["unknown, not allowed", { ...unknown, allowedActions: [] }, [own], []],
    // Allowed whatever the accounts' read says.
    ["gave-up, accounts loading", failed("gave-up"), undefined, ["send-again"]],
    // Gmail would refuse it again, so it is edited, from any account that sends.
    ["refused", failed("refused"), [own], ["edit"]],
    ["refused, accounts loading", failed("refused"), undefined, []],
    ["refused, no account sends", failed("refused"), [{ ...own, allowedActions: [] }], []],
    ["refused, its account stale", failed("refused"), [stale, other], ["edit", "reconnect"]],
    // The account's state now, not the failure's, decides the reconnect.
    [
      "needs-reconnect, still",
      { ...failed("needs-reconnect"), allowedActions: [] },
      [stale],
      ["reconnect"],
    ],
    ["needs-reconnect, reconnected", failed("needs-reconnect"), [own], ["send-again"]],
    [
      "token-unreadable, still",
      { ...failed("token-unreadable"), allowedActions: [] },
      [{ ...stale, status: "token-unreadable" }],
      ["reconnect"],
    ],
    [
      "gave-up, account stale since",
      { ...failed("gave-up"), allowedActions: [] },
      [stale],
      ["reconnect"],
    ],
    ["unknown, account stale since", { ...unknown, allowedActions: [] }, [stale], ["reconnect"]],
    // A disconnected account can't be reconnected from its old messages.
    [
      "needs-reconnect, disconnected",
      { ...failed("needs-reconnect"), allowedActions: [] },
      [other],
      [],
    ],
  ])("offers what a %s message allows", (_, message, accounts, offers) => {
    expect(nextSteps(message, accounts).offers).toEqual(offers);
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
    // The API still allows it, as it returns the same copy.
    expect(nextSteps({ ...message, allowedActions: ["send-again"] }, [stale])).toEqual({
      text,
      offers: [],
    });
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
    // Facts, not offers: one the API won't send again now still waits.
    expect(unfinished({ ...failed("gave-up"), allowedActions: [] })).toBe(true);
    expect(unfinished(failed("refused"))).toBe(true);
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

describe("invalidAddressMessage", () => {
  it("ends with the value as typed, so its own quotes read as typed", () => {
    expect(invalidAddressMessage('"Lopez, Ana"')).toBe(
      'This isn\'t an email address, so fix it or remove it: "Lopez, Ana"',
    );
  });
});

describe("why no account can send", () => {
  it("asks to connect one when there is none", () => {
    expect(noAccountText([])).toBe("Connect a Gmail account to send messages from it.");
  });

  it("says to reconnect when each account needs it", () => {
    expect(noAccountText([{ status: "needs-reconnect" }, { status: "token-unreadable" }])).toBe(
      "Each of your Gmail accounts needs reconnecting before it can send.",
    );
    expect(cannotSendNote({ email: "studio@example.com", status: "needs-reconnect" })).toBe(
      "studio@example.com needs reconnecting before it can send. Choose another account, or reconnect it on the Connected accounts page.",
    );
  });

  // Usable accounts the server lists unable to send: Gmail isn't set up,
  // which Connected accounts words for whoever reads it.
  it("says they can't send yet when they are usable", () => {
    expect(noAccountText([{ status: "needs-reconnect" }, { status: "usable" }])).toBe(
      "Your Gmail accounts can't send yet.",
    );
    expect(cannotSendNote({ email: "studio@example.com", status: "usable" })).toBe(
      "Your Gmail accounts can't send yet. The Connected accounts page says why.",
    );
  });
});

describe("an Outbox row's status", () => {
  const copy = "0b9f3a52-5f1d-4a8e-9a43-5f0c2b6b1c11";

  it.each<[string, MessageState, string, string, string]>([
    ["waiting", waiting, "Waiting", "neutral", ""],
    ["sending", { ...waiting, status: "sending" }, "Sending", "info", ""],
    ["sent", { ...waiting, status: "sent" }, "Sent", "success", ""],
    [
      "unknown",
      { ...waiting, status: "unknown" },
      "Not confirmed",
      "warning",
      "Check Sent in Gmail before sending again.",
    ],
    [
      "refused",
      failed("refused"),
      "Not sent",
      "destructive",
      "Gmail refused it. Edit it and send it as new.",
    ],
    [
      "sending-limit",
      failed("sending-limit"),
      "Not sent",
      "destructive",
      "Its account hit Gmail's daily limit. Send it again later.",
    ],
    [
      "needs-reconnect",
      failed("needs-reconnect"),
      "Not sent",
      "destructive",
      "Reconnect the account, then send it again.",
    ],
    [
      "token-unreadable",
      failed("token-unreadable"),
      "Not sent",
      "destructive",
      "Reconnect the account, then send it again.",
    ],
    [
      "deactivated",
      failed("deactivated"),
      "Not sent",
      "destructive",
      "Your account was deactivated. Send it again.",
    ],
    [
      "sender-changed",
      failed("sender-changed"),
      "Not sent",
      "destructive",
      "The account's address changed. Send it again.",
    ],
    [
      "gave-up",
      failed("gave-up"),
      "Not sent",
      "destructive",
      "Google didn't take it. Send it again.",
    ],
    // Sent again, whatever it failed for or how, it asks nothing more.
    [
      "failed, sent again",
      { ...failed("needs-reconnect"), sentAgainAs: copy },
      "Tried again",
      "neutral",
      "You sent it again as a new message.",
    ],
    [
      "unknown, sent again",
      { ...waiting, status: "unknown", sentAgainAs: copy },
      "Tried again",
      "neutral",
      "You sent it again as a new message.",
    ],
  ])("words a %s message", (_, message, label, tone, reason) => {
    expect(messageLook(message)).toMatchObject({ label, tone });
    expect(rowReason(message)).toBe(reason);
  });
});
