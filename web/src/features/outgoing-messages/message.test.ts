import { describe, expect, it } from "vite-plus/test";
import type { OutgoingMessage } from "@/api/generated/model";
import { messageContent, messageHead } from "./message";

const message: OutgoingMessage = {
  id: "0b9f3a52-5f1d-4a8e-9a43-5f0c2b6b1c11",
  connectedAccountId: 9,
  from: "me@example.com",
  to: ["ana@example.com"],
  cc: ["bo@example.com"],
  bcc: ["cy@example.com"],
  subject: "Lunch",
  body: "Noon?",
  status: "waiting",
  failure: null,
  createdAt: "2026-11-10T10:00:00Z",
  sentAt: null,
  sentAgainAs: null,
  dismissedAt: null,
  outstanding: true,
  reply: false,
  allowedActions: [],
};

describe("messageHead", () => {
  it("shows every list, Bcc included, sent, dated when the message was saved", () => {
    expect(messageHead(message)).toEqual({
      from: [{ name: "", address: "me@example.com" }],
      replyTo: [],
      to: [{ name: "", address: "ana@example.com" }],
      cc: [{ name: "", address: "bo@example.com" }],
      bcc: [{ name: "", address: "cy@example.com" }],
      date: "2026-11-10T10:00:00Z",
      sent: true,
    });
  });

  it("dates a sent message when Gmail took it", () => {
    const sent = { ...message, status: "sent" as const, sentAt: "2026-11-10T10:00:05Z" };
    expect(messageHead(sent).date).toBe("2026-11-10T10:00:05Z");
  });
});

describe("messageContent", () => {
  it("is the plain-text body, loaded, with no attachments", () => {
    expect(messageContent(message)).toEqual({
      status: "success",
      bodies: [{ partId: "body", type: "text", text: "Noon?" }],
      remote: "none",
      attachments: [],
    });
  });
});
