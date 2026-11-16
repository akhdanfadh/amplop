import type { OutgoingMessage } from "@/api/generated/model";
import type { OpenMessageHead } from "@/features/messages/open-message";
import type { MessageContent } from "@/features/messages/view";
import type { MailAddress } from "./reply-defaults";

// messageHead returns the header of message as an open message shows it:
// from its account's address, sent, and dated when Gmail accepted it, or
// else when it was saved.
export function messageHead(message: OutgoingMessage): OpenMessageHead {
  return {
    from: addresses([message.from]),
    replyTo: [],
    to: addresses(message.to),
    cc: addresses(message.cc),
    bcc: addresses(message.bcc),
    date: message.sentAt ?? message.createdAt,
    sent: true,
  };
}

// messageContent returns message's plain-text body as the message view
// shows it, already loaded.
export function messageContent(message: OutgoingMessage): MessageContent {
  return {
    status: "success",
    bodies: [{ partId: "body", type: "text", text: message.body }],
    remote: "none",
    attachments: [],
  };
}

// addresses returns emails as header addresses, which have no display names.
function addresses(emails: readonly string[]): MailAddress[] {
  return emails.map((address) => ({ name: "", address }));
}
