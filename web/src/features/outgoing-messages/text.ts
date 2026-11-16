import type { ListedAccount, OutgoingMessageSummary, ProblemType } from "@/api/generated/model";
import { uncertainMessage, uncertainOutcome, userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import type { LeaveWarningText } from "@/components/leave-warning";
import { shortcutModifier } from "@/lib/shortcut";

type Status = OutgoingMessageSummary["status"];
type Failure = NonNullable<OutgoingMessageSummary["failure"]>;

const notFound = 404;
const conflict = 409;
const unprocessable = 422;
const keyReusedType: ProblemType = "/api/v0/problems/idempotency-key-reused";
const needsReconnectType: ProblemType = "/api/v0/problems/sending-account-needs-reconnect";
const notSendableAgainType: ProblemType = "/api/v0/problems/not-sendable-again";
const sentAgainOtherwiseType: ProblemType = "/api/v0/problems/sent-again-otherwise";
const messageNotFoundType: ProblemType = "/api/v0/problems/outgoing-message-not-found";
const answeredNotFoundType: ProblemType = "/api/v0/problems/message-not-found";
export const messageGone = "This message isn't in your Outbox. It may have been removed.";

// The badge of a message sent again as a copy.
export const sentAgainLabel = "Tried again";

// The short name of each status, e.g. for a badge.
const statusLabels: Record<Status, string> = {
  waiting: "Waiting",
  sending: "Sending",
  sent: "Sent",
  failed: "Not sent",
  unknown: "Not confirmed",
};

// statusLabel returns the short name of status.
export function statusLabel(status: Status): string {
  return statusLabels[status];
}

// What a failed message's reason asks of the member. from is the address
// the message was saved with; current is the account's address now, which
// Send again uses, when known.
const failures: Record<Failure, (from: string, current: string | undefined) => string> = {
  refused: () =>
    "Gmail refused this message. Check its recipients and content, then edit and send it.",
  "sending-limit": (from) =>
    `${from} reached Gmail's daily sending limit, which lifts within 24 hours. Send it again then.`,
  "needs-reconnect": (from) =>
    `Google stopped accepting inbok's access to ${from}. Reconnect the account on the Connected accounts page, then send it again.`,
  "token-unreadable": (from) =>
    `inbok can no longer use the access it saved for ${from}. Reconnect the account on the Connected accounts page, then send it again.`,
  deactivated: () =>
    "Your inbok account was deactivated when this message was due to go out. Send it again now.",
  "sender-changed": (_, current) =>
    current
      ? `Gmail now uses another address for this account. Sending it again sends it from ${current}.`
      : "Gmail now uses another address for this account. Sending it again sends it from the new address.",
  "gave-up": () =>
    "Google didn't take this message after several tries, so inbok stopped. Send it again.",
};

// MessageState is what a status page needs of a message to word it and
// to know what it offers.
export type MessageState = Pick<
  OutgoingMessageSummary,
  "status" | "failure" | "from" | "sentAt" | "sentAgainAs" | "allowedActions" | "connectedAccountId"
>;

// SendingAccount is what nextSteps needs of each of the member's accounts.
export type SendingAccount = Pick<ListedAccount, "id" | "email" | "status" | "allowedActions">;

// What a message in each status other than failed says.
const statusTexts: Record<Exclude<Status, "failed">, (from: string) => string> = {
  waiting: () => "Waiting to be sent.",
  sending: () => "Sending...",
  sent: () => "Sent.",
  unknown: (from) =>
    `inbok couldn't confirm whether Gmail sent this message. Check the Sent folder of ${from} in Gmail before sending it again.`,
};

// What the member can do with a message from its page: send it again in
// one step, edit it into a new message, or reconnect its account.
export type Offer = "send-again" | "edit" | "reconnect";

// NextSteps is what a message's page says happened and what it offers.
export type NextSteps = { text: string; offers: Offer[] };

// What a failed message's text starts with, which a summary that names
// the status beside it leaves out.
export const notSentPrefix = "Not sent. ";

// sentSummary words message's status in one line, for the compose window
// after Send: who it is going or went to, or the status's name, Tried
// again once it was sent again.
export function sentSummary(
  message: MessageState & Pick<OutgoingMessageSummary, "to" | "cc" | "bcc">,
): string {
  if (message.sentAgainAs !== null) return sentAgainLabel;
  if (inProgress(message.status)) return `Sending to ${recipientSummary(message)}...`;
  if (message.status === "sent") return `Sent to ${recipientSummary(message)}`;
  return statusLabel(message.status);
}

// A message sent again already has a copy: it offers nothing more, so no
// text sends the member to send it twice.
const sentAgainTexts: Partial<Record<Status, string>> = {
  failed: `${notSentPrefix}You already tried sending it again.`,
  unknown:
    "inbok couldn't confirm whether Gmail sent this message. You already tried sending it again.",
};

// nextSteps says what happened to message and what the member can do
// next. accounts are the member's connected accounts, undefined until they
// load; the sending account's address now names where Send again sends
// from.
export function nextSteps(message: MessageState, accounts?: readonly SendingAccount[]): NextSteps {
  const again = message.sentAgainAs === null ? undefined : sentAgainTexts[message.status];
  if (again !== undefined) return { text: again, offers: [] };
  const offers = endedOffers(message, accounts);
  if (message.status === "unknown") return { text: statusTexts.unknown(message.from), offers };
  if (message.status !== "failed")
    return { text: statusTexts[message.status](message.from), offers: [] };
  const current = sendingAccount(message, accounts)?.email;
  const failure = message.failure && failures[message.failure];
  const text = failure ? `${notSentPrefix}${failure(message.from, current)}` : "Not sent.";
  return { text, offers };
}

// endedOffers returns what a failed or unknown message, not yet sent
// again, offers while the API allows sending it again: Send again, or for
// a refused one, which Gmail would refuse again unchanged, Edit and send,
// which sends it again with the member's edits; and the Connected accounts
// page while the sending account is one a reconnect fixes.
function endedOffers(message: MessageState, accounts?: readonly SendingAccount[]): Offer[] {
  const offers: Offer[] = [];
  if (message.allowedActions.includes("send-again"))
    offers.push(message.failure === "refused" ? "edit" : "send-again");
  const account = sendingAccount(message, accounts);
  if (account !== undefined && account.status !== "usable") offers.push("reconnect");
  return offers;
}

// sendingAccount returns message's account among accounts, or undefined
// while they load or once it was disconnected.
function sendingAccount(
  message: Pick<MessageState, "connectedAccountId">,
  accounts?: readonly SendingAccount[],
): SendingAccount | undefined {
  return accounts?.find((account) => account.id === message.connectedAccountId);
}

// inProgress reports whether status can still change by itself.
export function inProgress(status: Status): boolean {
  return status === "waiting" || status === "sending";
}

// keyReused reports whether err is the API's refusal of an Idempotency-Key
// that saved another message: an earlier try's, before the member's edits.
export function keyReused(err: ApiError | null): boolean {
  return err?.status === unprocessable && err.problem?.type === keyReusedType;
}

// needsReconnect reports whether err is the API's refusal of an account
// that must be reconnected before it sends.
export function needsReconnect(err: ApiError | null): boolean {
  return err?.status === conflict && err.problem?.type === needsReconnectType;
}

// recipientSummary names the first recipient of a message and how many
// others it has, e.g. "ana@example.com and 2 others".
export function recipientSummary(
  message: Pick<OutgoingMessageSummary, "to" | "cc" | "bcc">,
): string {
  const all = [...message.to, ...message.cc, ...message.bcc];
  const [first] = all;
  if (first === undefined) return "No recipients";
  const others = all.length - 1;
  if (others === 0) return first;
  return `${first} and ${others} ${others === 1 ? "other" : "others"}`;
}

// sendErrorMessage words a Send Message that failed.
export function sendErrorMessage(err: ApiError): string {
  if (err.status === unprocessable && err.problem?.type === keyReusedType)
    return "An earlier try may have saved this message before your last edits. Check the Outbox before sending again.";
  if (uncertainOutcome(err))
    // The request's Idempotency-Key makes a repeat return what the first saved.
    return uncertainMessage(
      err,
      "We couldn't confirm whether your message was saved. Sending again is safe: it won't be sent twice.",
    );
  return userMessage(err);
}

// replyErrorMessage words a Reply to Message that failed.
export function replyErrorMessage(err: ApiError): string {
  if (err.status === notFound && err.problem?.type === answeredNotFoundType)
    return "The message you're replying to was deleted, so this reply can't be sent. Copy what you typed to keep it.";
  if (keyReused(err))
    return "An earlier try may have saved this reply before your last edits. Check the Outbox before sending again.";
  if (uncertainOutcome(err))
    // The request's Idempotency-Key makes a repeat return what the first saved.
    return uncertainMessage(
      err,
      "We couldn't confirm whether your reply was saved. Sending again is safe: it won't be sent twice.",
    );
  return userMessage(err);
}

// What From says when the chosen account didn't receive the answered
// message: Gmail threads a reply only within the account that holds it.
export const otherAccountNote =
  "This account didn't receive the message, so the reply starts a new conversation in its Gmail.";

// What the reply part of a conversation says when the member's accounts
// could not be read, before the reason.
export const accountsUnreadable = "Could not load your connected accounts, so you can't reply yet.";

// What the reply part says for a message whose ID is not one: it is never
// put in the API's path.
export const notAnswerable = "This message can't be answered here.";

// What the compose window's status says, before the reason, when reading
// the sent message again failed: the status shown may have moved on.
export const messageStatusStale =
  "Could not read this message's status again, so it may be out of date.";

// What the compose window says when the connected accounts can't be read,
// before the reason.
export const composeAccountsUnreadable = "Could not load your connected accounts.";

// noAccountText says why the compose window has no editor: no connected
// account, or none of accounts able to send, because each needs
// reconnecting, or else, usable as they are, because Gmail isn't set up on
// this inbok, which the Connected accounts page it links to words for
// whoever reads it.
export function noAccountText(accounts: readonly Pick<ListedAccount, "status">[]): string {
  if (accounts.length === 0) return "Connect a Gmail account to send messages from it.";
  if (accounts.some((account) => account.status === "usable"))
    return "Your Gmail accounts can't send yet.";
  return "Each of your Gmail accounts needs reconnecting before it can send.";
}

// What the compose window says after a 401, before the link to sign in.
export const signedOutText =
  "You're signed out, so this message can't be sent. Copy what you typed, then";

// What unfolds a refused reply's subject, cut to 2 lines in the editor.
export const showAllSubject = "Show all";

// What heads an editor sending a refused message again with the member's
// edits, which then replaces it in the Outbox.
export const editNote =
  "Gmail refused this message. Check its recipients and content, then send: it replaces the refused one in the Outbox.";

// What Edit and send asks while the compose window holds another message.
export const replaceText: LeaveWarningText = {
  title: "Replace the message you're writing?",
  description: "What you typed in it hasn't been sent, and it will be lost.",
  stay: "Keep writing",
  leave: "Replace",
  leaveVariant: "destructive",
};

// What a reply's status says, before the reason, when reading it again
// failed: the status shown may have moved on.
export const replyStatusStale =
  "Could not read this reply's status again, so it may be out of date.";

// invalidAddressMessage says value, in a recipient list, isn't an address.
// The value ends the message, unquoted, so quotes of its own, e.g. in
// "Lopez, Ana", read as typed.
export function invalidAddressMessage(value: string): string {
  return `This isn't an email address, so fix it or remove it: ${value}`;
}

// cannotSendNote says why the account that received the message can't
// send, so no account is chosen: it needs reconnecting, or else, usable as
// it is, no account can send until Gmail is set up on this inbok, which
// the Connected accounts page words for whoever reads it.
export function cannotSendNote(account: Pick<ListedAccount, "email" | "status">): string {
  return account.status === "usable"
    ? "Your Gmail accounts can't send yet. The Connected accounts page says why."
    : `${account.email} needs reconnecting before it can send. Choose another account, or reconnect it on the Connected accounts page.`;
}

// sendTooltip is the Send button's tooltip: its name and the keys that
// send, as the platform's keyboard writes them (DESIGN.md "Wording").
export function sendTooltip(): string {
  return `Send (${shortcutModifier()}+Enter)`;
}

// What the reply editor's buttons say: each opens the editor for one mode.
export const replyLabels = { reply: "Reply", "reply-all": "Reply all" } as const;

// sendAgainErrorMessage words a Send Message Again that failed.
export function sendAgainErrorMessage(err: ApiError): string {
  // Only the API's own problems say why; a missing route says nothing.
  if (err.status === conflict && err.problem?.type === notSendableAgainType)
    return "This message can't be sent again: its status has changed.";
  if (err.status === conflict && err.problem?.type === sentAgainOtherwiseType)
    return "This message was already sent again with other content. Open it in the Outbox to see that copy.";
  if (err.status === notFound && err.problem?.type === messageNotFoundType) return messageGone;
  if (uncertainOutcome(err))
    // A repeat returns the copy the first saved.
    return uncertainMessage(
      err,
      "We couldn't confirm whether it was sent again. Trying again is safe: it's sent again at most once.",
    );
  return userMessage(err);
}

// readErrorMessage words a failed read of one message.
export function readErrorMessage(err: ApiError): string {
  if (err.status === notFound && err.problem?.type === messageNotFoundType) return messageGone;
  return `Could not load this message. ${userMessage(err)}`;
}

// The compose window's name until its message has a subject.
export const newMessageTitle = "New message";

// closeText returns what closing the compose window asks in state, from
// its Close or Discard, or from Back on a phone.
export function closeText(state: LeaveState): LeaveWarningText {
  if (state === "sending") return closeSending;
  if (state === "unconfirmed") return closeUnconfirmed;
  return leaveTyped;
}

const closeSending: LeaveWarningText = {
  title: "Close while sending?",
  description:
    "Your message is still being saved. If you close it, check the Outbox to see whether it was.",
  stay: "Keep open",
  leave: "Close",
  leaveVariant: "default",
};
const closeUnconfirmed: LeaveWarningText = {
  title: "Close this message?",
  description:
    "An earlier try may have saved this message, and it may be sent. What you typed won't be kept.",
  stay: "Keep open",
  leave: "Close",
  leaveVariant: "default",
};

// signOutText returns what signing out asks while the compose window would
// lose something in state.
export function signOutText(state: LeaveState): LeaveWarningText {
  if (state === "sending") return signOutSending;
  if (state === "unconfirmed") return signOutUnconfirmed;
  return signOutTyped;
}

const signOutTyped: LeaveWarningText = {
  title: "Sign out and discard your message?",
  description: "The message you're writing hasn't been sent, and it will be lost.",
  stay: "Keep writing",
  leave: "Sign out",
  leaveVariant: "destructive",
};
const signOutSending: LeaveWarningText = {
  title: "Sign out while sending?",
  description:
    "Your message is still being saved. If you sign out, check the Outbox after signing in to see whether it was.",
  stay: "Stay",
  leave: "Sign out",
  leaveVariant: "default",
};
const signOutUnconfirmed: LeaveWarningText = {
  title: "Sign out and close your message?",
  description:
    "An earlier try may have saved this message, and it may be sent. What you typed won't be kept.",
  stay: "Stay",
  leave: "Sign out",
  leaveVariant: "default",
};

// What leaving a message editor's page asks, e.g. the reply's: while it
// saves; after a try that may have saved the message; or with typed input
// that was surely not sent.
const leaveSending: LeaveWarningText = {
  title: "Leave while sending?",
  description:
    "Your message is still being saved. If you leave, check the Outbox to see whether it was.",
  stay: "Stay",
  leave: "Leave",
  leaveVariant: "default",
};
// The member may be leaving to check the Outbox, as the error says, so this
// does not assume where they go.
const leaveUnconfirmed: LeaveWarningText = {
  title: "Leave this page?",
  description:
    "An earlier try may have saved this message, and it may be sent. What you typed here won't be kept.",
  stay: "Stay",
  leave: "Leave",
  leaveVariant: "default",
};
const leaveTyped: LeaveWarningText = {
  title: "Discard this message?",
  description: "It hasn't been sent, and what you typed will be lost.",
  stay: "Keep editing",
  leave: "Discard",
  leaveVariant: "destructive",
};

// mayHaveSaved reports whether err leaves a message possibly saved: a lost
// answer, or a key an earlier try used before the member's edits.
export function mayHaveSaved(err: ApiError): boolean {
  return uncertainOutcome(err) || keyReused(err);
}

// The state of a message editor that decides what leaving it asks: a send
// running; an earlier try that may have saved the message; or typed input
// that surely was not sent.
export type LeaveState = "sending" | "unconfirmed" | "typed";

// What discarding a reply with typed text asks, from its Discard button or
// by leaving the page.
const discardReply: LeaveWarningText = {
  ...leaveTyped,
  title: "Discard this reply?",
};

// What Discard asks in the reply editor after a try that may have saved
// the reply: Discard then only drops the editor, not the reply.
const discardReplyUnconfirmed: LeaveWarningText = {
  title: "Discard this reply?",
  description:
    "An earlier try may have saved this reply, and it may be sent. What you typed here won't be kept.",
  stay: "Keep editing",
  leave: "Discard",
  leaveVariant: "default",
};

// discardReplyText returns what the reply editor's Discard asks: whether
// an earlier try may have saved the reply (unconfirmed), or only that
// typed text will be lost.
export function discardReplyText(unconfirmed: boolean): LeaveWarningText {
  return unconfirmed ? discardReplyUnconfirmed : discardReply;
}

// replyLeaveText returns what leaving the reply editor asks in state.
export function replyLeaveText(state: LeaveState): LeaveWarningText {
  return state === "typed" ? discardReply : leaveText(state);
}

// leaveText returns what leaving a message editor's page asks in state.
export function leaveText(state: LeaveState): LeaveWarningText {
  if (state === "sending") return leaveSending;
  if (state === "unconfirmed") return leaveUnconfirmed;
  return leaveTyped;
}

// What a row says after the status of a message in each failure, short
// for a line; the message's page keeps the whole sentence (nextSteps).
const shortFailures: Record<Failure, string> = {
  refused: "Gmail refused it. Edit and send it again.",
  "sending-limit": "Its account hit Gmail's daily limit. Send it again later.",
  "needs-reconnect": "Reconnect the account, then send it again.",
  "token-unreadable": "Reconnect the account, then send it again.",
  deactivated: "Your account was deactivated. Send it again.",
  "sender-changed": "The account's address changed. Send it again.",
  "gave-up": "Google didn't take it. Send it again.",
};

// rowReason returns what an Outbox row says after message's status: why
// it failed, what an unconfirmed one asks, or that it was sent again;
// empty while it waits, sends, or once it was sent, which the status says
// alone.
export function rowReason(message: MessageState): string {
  if (message.sentAgainAs !== null) return "You sent it again as a new message.";
  if (message.status === "unknown") return "Check Sent in Gmail before sending again.";
  if (message.status !== "failed") return "";
  return message.failure ? shortFailures[message.failure] : "";
}

// outboxRowName returns what a screen reader calls an Outbox row: its
// recipients, subject, status, and date, and its account when the list
// mixes accounts.
export function outboxRowName(row: {
  to: string;
  subject: string;
  status: string;
  date: string;
  account: string | undefined;
}): string {
  return [`${rowTo} ${row.to}`, row.subject, row.status, row.date, row.account]
    .filter(Boolean)
    .join(", ");
}

// What an Outbox row's recipients follow.
export const rowTo = "To";

// The Outbox's heading, and its list's name for a screen reader.
export const outboxName = "Outbox";
export const outboxListName = "Messages sent from inbok";
// What the Outbox's reading pane says with no message open.
export const noMessageOpen = "No message open.";
// The Outbox list's sections: the messages that still need the member
// (the API's outstanding), the sidebar count's words, then the rest.
export const outboxSections = { outstanding: "Not sent yet", done: "Done" } as const;
// What ends the Outbox's list: what it holds, and what it doesn't.
export const outboxEnd =
  "inbok keeps up to 50 messages here, dropping the oldest done ones first. What you send from Gmail itself is in Sent, not here.";
// What an empty Outbox says.
export const outboxEmptyTitle = "Nothing sent from inbok yet";
export const outboxEmptyText =
  "Messages you send from inbok show here, with whether Gmail took each one. Write one with Compose.";
// What the Outbox says while its list loads, for a screen reader.
export const outboxLoading = "Loading messages...";
// What the Outbox says when its list can't be read, before the reason,
// and when a reread failed, so the list may be out of date.
export const outboxFailed = "Could not load your messages.";
export const outboxStale = "Could not reload your messages, so the list may be out of date.";
// The account line of an open outgoing message's details.
export const sentFrom = "Sent from";
// What a message's reading pane says while it loads.
export const messageLoading = "Loading message...";
