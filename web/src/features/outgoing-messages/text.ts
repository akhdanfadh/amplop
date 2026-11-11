import type { OutgoingMessageSummary, ProblemType } from "@/api/generated/model";
import { uncertainMessage, uncertainOutcome, userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import type { LeaveWarningText } from "@/components/leave-warning";

type Status = OutgoingMessageSummary["status"];
type Failure = NonNullable<OutgoingMessageSummary["failure"]>;

const notFound = 404;
const conflict = 409;
const unprocessable = 422;
const keyReusedType: ProblemType = "/api/v0/problems/idempotency-key-reused";
const needsReconnectType: ProblemType = "/api/v0/problems/sending-account-needs-reconnect";
const notSendableAgainType: ProblemType = "/api/v0/problems/not-sendable-again";
const messageNotFoundType: ProblemType = "/api/v0/problems/outgoing-message-not-found";
export const messageGone = "This message isn't in your Outbox. It may have been removed.";

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
    "Gmail refused this message. Check its recipients and content, then edit it and send it as a new message.",
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

// MessageState is what a status page needs of a message to word it.
export type MessageState = Pick<
  OutgoingMessageSummary,
  "status" | "failure" | "from" | "sentAt" | "sentAgainAs"
>;

// What a message in each status other than failed says.
const statusTexts: Record<Exclude<Status, "failed">, (from: string) => string> = {
  waiting: () => "Waiting to be sent.",
  sending: () => "Sending...",
  sent: () => "Sent.",
  unknown: (from) =>
    `inbok couldn't confirm whether Gmail sent this message. Check the Sent folder of ${from} in Gmail before sending it again.`,
};

// statusText says what happened to message and what the member can do.
// current is the sending account's address now, when known.
export function statusText(message: MessageState, current?: string): string {
  if (message.status !== "failed") return statusTexts[message.status](message.from);
  const failure = message.failure && failures[message.failure];
  return failure ? `Not sent. ${failure(message.from, current)}` : "Not sent.";
}

// inProgress reports whether status can still change by itself.
export function inProgress(status: Status): boolean {
  return status === "waiting" || status === "sending";
}

// statusVariant returns the badge variant for status: destructive for a
// message not sent or not confirmed, which asks something of the member.
export function statusVariant(status: Status): "destructive" | "secondary" {
  return status === "failed" || status === "unknown" ? "destructive" : "secondary";
}

// reconnectAdvised reports whether message failed for a reason that
// reconnecting its account fixes.
export function reconnectAdvised(message: Pick<MessageState, "failure">): boolean {
  return message.failure === "needs-reconnect" || message.failure === "token-unreadable";
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

// sendableAgain reports whether the member can send message again in one
// step: a failed or unknown message not sent again yet. A refused one is
// edited into a new message instead, since Gmail would refuse it again.
export function sendableAgain(message: MessageState): boolean {
  if (message.sentAgainAs !== null) return false;
  if (message.status === "unknown") return true;
  return message.status === "failed" && message.failure !== "refused";
}

// editable reports whether the member is offered to edit message into a
// new one: a refused message, which sending again would not fix.
export function editable(message: MessageState): boolean {
  return message.status === "failed" && message.failure === "refused";
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

// sendAgainErrorMessage words a Send Message Again that failed.
export function sendAgainErrorMessage(err: ApiError): string {
  // Only the API's own problems say why; a missing route says nothing.
  if (err.status === conflict && err.problem?.type === notSendableAgainType)
    return "This message can't be sent again: its status has changed.";
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

// What leaving the compose form asks: while it saves; after a try that may
// have saved the message; or with typed input that was surely not sent.
const leaveSending: LeaveWarningText = {
  title: "Leave while sending?",
  description:
    "Your message is still being saved. If you leave, check the Outbox to see whether it was.",
  stay: "Stay",
  leave: "Leave",
  leaveVariant: "default",
};
const leaveUnconfirmed: LeaveWarningText = {
  title: "Leave without checking?",
  description:
    "An earlier try may have saved this message, and it may be sent. Check the Outbox before writing it again.",
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

// The state of the compose form that decides what leaving it asks: a send
// running; an earlier try that may have saved the message; or typed input
// that surely was not sent.
export type LeaveState = "sending" | "unconfirmed" | "typed";

// leaveText returns what leaving the compose form asks in state.
export function leaveText(state: LeaveState): LeaveWarningText {
  if (state === "sending") return leaveSending;
  if (state === "unconfirmed") return leaveUnconfirmed;
  return leaveTyped;
}
