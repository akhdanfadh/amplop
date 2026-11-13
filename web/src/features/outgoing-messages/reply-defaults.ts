import type { ConnectedAccount } from "@/api/generated/model";

// MailAddress is one address of a synchronized message, as the conversation
// API gives it: a display name, often empty, and the address.
export type MailAddress = { name: string; address: string };

// AnsweredMessage is what a reply needs of the message it answers: its ID,
// the account that holds it, its senders and recipients, and whether that
// account sent it (it is in the account's Sent mail).
//
// DESIGN: it has the shape of branch 3's ConversationMessage plus its
// account and the per-message sent flag, so the conversation view passes
// one in as it is.
export type AnsweredMessage = {
  id: string;
  accountId: number;
  from: MailAddress[];
  to: MailAddress[];
  cc: MailAddress[];
  replyTo: MailAddress[];
  sent: boolean;
};

// ReplyMode is which button opened the editor.
export type ReplyMode = "reply" | "reply-all";

// What the reply editor holds. Each recipient list holds its addresses as
// the member entered them, valid or not; connectedAccountId is 0 until an
// account is chosen.
export type ReplyValues = {
  connectedAccountId: number;
  to: string[];
  cc: string[];
  bcc: string[];
  body: string;
};

// replyDefaults returns what the reply editor starts with when the member
// answers message in mode, given accounts, the member's connected accounts.
// It sends from the account that received message while that account can
// send (US009), else from none until the member chooses. The recipients
// are Gmail's:
//   - Reply goes to Reply-To, else to From (RFC 5322 section 3.6.3).
//   - Reply all also copies message's To and Cc.
//   - Answering a message the account itself sent goes to that message's
//     To, and Reply all copies its Cc.
// Reply all leaves out the receiving account's address and any address
// already listed, comparing addresses in any case and keeping the first
// spelling.
//
// DESIGN: only the receiving account is left out. The member's other
// accounts and Gmail aliases stay in Cc, as the member may want those
// copies; the editor shows every address before sending.
export function replyDefaults(
  message: AnsweredMessage,
  mode: ReplyMode,
  accounts: readonly ConnectedAccount[],
): ReplyValues {
  const account = accounts.find((candidate) => candidate.id === message.accountId);
  const own = account?.email;
  const to = message.sent
    ? message.to
    : message.replyTo.length > 0
      ? message.replyTo
      : message.from;
  const copied = mode === "reply" ? [] : message.sent ? message.cc : [...message.to, ...message.cc];
  const seen = new Set<string>();
  const toList = distinct(to, seen);
  if (own !== undefined) seen.add(own.toLowerCase());
  const ccList = distinct(copied, seen);
  return {
    connectedAccountId: account?.status === "usable" ? account.id : 0,
    to: toList,
    cc: ccList,
    bcc: [],
    body: "",
  };
}

// othersReceived reports whether Reply all would reach someone Reply
// leaves out, so the conversation offers it (DESIGN.md, "Reading pane").
export function othersReceived(
  message: AnsweredMessage,
  accounts: readonly ConnectedAccount[],
): boolean {
  return replyDefaults(message, "reply-all", accounts).cc.length > 0;
}

// distinct returns the addresses of list not yet in seen, in order, and
// adds each to seen, lower-cased.
function distinct(list: readonly MailAddress[], seen: Set<string>): string[] {
  const addresses: string[] = [];
  for (const { address } of list) {
    const key = address.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    addresses.push(address);
  }
  return addresses;
}
