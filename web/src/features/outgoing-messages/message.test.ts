import { describe, expect, it } from "vite-plus/test";
import type { OutgoingMessage } from "@/api/generated/model";
import { messageContent, messageHeader } from "./message";

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
};

describe("messageHeader", () => {
  it("shows every list, Bcc included, dated when the message was saved", () => {
    expect(messageHeader(message)).toEqual({
      from: [{ email: "me@example.com" }],
      to: [{ email: "ana@example.com" }],
      cc: [{ email: "bo@example.com" }],
      bcc: [{ email: "cy@example.com" }],
      subject: "Lunch",
      receivedAt: "2026-11-10T10:00:00Z",
    });
  });

  it("dates a sent message when Gmail took it", () => {
    const sent = { ...message, status: "sent" as const, sentAt: "2026-11-10T10:00:05Z" };
    expect(messageHeader(sent).receivedAt).toBe("2026-11-10T10:00:05Z");
  });
});

describe("messageContent", () => {
  it("is the plain-text body, loaded", () => {
    expect(messageContent(message)).toEqual({
      status: "success",
      bodies: [{ partId: "body", type: "text", text: "Noon?" }],
      remote: "none",
    });
  });
});
