import type { OutgoingMessage } from "@/api/generated/model";
import type { Address } from "@/features/messages/text";
import type { MessageContent, MessageHeader } from "@/features/messages/view";

// messageHeader returns the header fields of message as the message view
// shows them, dated when Gmail accepted it, or else when it was saved.
export function messageHeader(message: OutgoingMessage): MessageHeader {
  return {
    from: [{ email: message.from }],
    to: addresses(message.to),
    cc: addresses(message.cc),
    bcc: addresses(message.bcc),
    subject: message.subject,
    receivedAt: message.sentAt ?? message.createdAt,
  };
}

// messageContent returns message's plain-text body as the message view
// shows it, already loaded.
export function messageContent(message: OutgoingMessage): MessageContent {
  return {
    status: "success",
    bodies: [{ partId: "body", type: "text", text: message.body }],
    remote: "none",
  };
}

// addresses returns emails as header addresses, which have no display names.
function addresses(emails: string[]): Address[] {
  return emails.map((email) => ({ email }));
}
