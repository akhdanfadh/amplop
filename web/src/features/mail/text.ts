import { formatCount } from "@/lib/count";
import type { ViewPlace } from "./places";

// Each view's name, as the sidebar and the list's heading show it.
export const placeName: Record<ViewPlace, string> = {
  inbox: "Inbox",
  starred: "Starred",
  sent: "Sent",
  all: "All mail",
  spam: "Spam",
  trash: "Trash",
};

// The sidebar's other names.
export const composeName = "Compose";
export const outboxName = "Outbox";

// What the Outbox's count counts, read after it.
export const notSentLabel = "not sent";

// Shown in the reading pane while no conversation is open.
export const nothingOpen = "No conversation open.";

// The list's name for a screen reader.
export const listName = "Conversations";

// Shown for a conversation whose subject is empty.
export const noSubject = "(no subject)";

// rowName returns what a screen reader calls a list row: whether it is
// unread, who, how many messages when more than one, its subject and date,
// whether it is starred, and its account when the list mixes accounts.
export function rowName(row: {
  who: string;
  count: number | undefined;
  subject: string;
  date: string;
  unread: boolean;
  starred: boolean;
  account: string | undefined;
}): string {
  return [
    row.unread && "Unread",
    row.who,
    row.count !== undefined && `${messageCount(row.count)} messages`,
    row.subject,
    row.date,
    row.starred && "Starred",
    row.account,
  ]
    .filter(Boolean)
    .join(", ");
}

// messageCount returns a number of messages as a row shows it, e.g.
// "1,284".
export function messageCount(count: number): string {
  return formatCount(count);
}

// How a row and a message's header call the account itself.
export const me = "me";

// A date on the day before today.
export const yesterday = "Yesterday";

// The reading pane's word before a message's recipients.
export const recipientsLabel = "To";

// The star button's name, which says whether it is pressed.
export const starName = "Star";

// starFailed returns what a failed star or unstar says, by starred, what
// it asked for.
export function starFailed(starred: boolean): string {
  return starred ? "Couldn't star the conversation" : "Couldn't remove the star";
}

// Why the API refused to change a conversation, with the fix.
export const refusedText = {
  "not-found": "It's no longer in Gmail.",
  "needs-reconnect": "Reconnect its account in Settings, then try again.",
  failed: "Gmail didn't answer. Try again.",
} as const;

// Who a row names when its newest messages carry no sender at all.
export const unknownSender = "Unknown sender";

// The list's end, once nothing older is left to load.
export const listEnd = "That's everything inbok has copied. Older mail stays in Gmail.";

// Read while a list's first page loads, and while it loads older ones.
export const listLoading = "Loading conversations...";
export const listLoadingMore = "Loading older conversations...";

// The phone's way from a conversation back to its list.
export const backToList = "Back";

// newConversations returns the button that shows conversations which
// arrived while the member was scrolled down.
export function newConversations(count: number): string {
  return count === 1 ? "1 new conversation" : `${formatCount(count)} new conversations`;
}

// The empty list's words (DESIGN.md "States").
export const noAccountsTitle = "Connect your first account";
export const noAccountsText = "inbok shows the mail of the accounts you connect.";
export const connectGmail = "Connect Gmail";

// emptyTitle returns what an empty list says of place, named name, its own
// name: a label's without its parents.
//
// DESIGN: the account and the label's parents stay out; the header above
// shows both, and a long account name and a deep label made the title run
// six lines on a phone.
export function emptyTitle(place: string, name: string): string {
  if (place === "spam") return "No spam";
  if (place === "trash") return "Trash is empty";
  return `Nothing in ${name}`;
}

// stillSyncing returns why an empty list may fill: account, or some
// account when undefined, is still copying its mail.
export function stillSyncing(account: string | undefined): string {
  return account === undefined
    ? "Some accounts are still syncing. Their mail appears here as it is copied."
    : `${account} is still syncing. Its mail appears here as it is copied.`;
}

// Failures of the list (DESIGN.md "States").
export const listFailed = "Couldn't load these conversations.";
export const olderFailedText = "Couldn't load older conversations.";
export const tryAgain = "Try again";
export function refreshFailed(since: string): string {
  return `Couldn't refresh. inbok isn't answering; showing mail from ${since}.`;
}

// The reading pane's words while its messages load, and when they fail.
export const messagesLoading = "Loading messages...";
export const messagesFailed = "Couldn't load this conversation's messages.";

// The sidebar's accounts: its group, and the item that shows every one.
export const accountsName = "Accounts";
export const allAccountsName = "All accounts";
// Said in the sidebar when the accounts could not be read.
export const accountsUnreadable = "Couldn't load your accounts.";
// What a place's or an account's count counts, read after it.
export const unreadLabel = "unread";

// The name of the chevron that folds an account's labels, and of the one
// that folds the labels under a label, at path. They differ so an account
// and a label of one name, e.g. Work, never share a button's name.
export function accountFoldName(account: string): string {
  return `Labels of ${account}`;
}
export function labelFoldName(path: string): string {
  return `Labels under ${path}`;
}

// The words an account's status shows beside it in the sidebar.
export const syncingWords = "Syncing";
export const reconnectWords = "Reconnect";
export const syncFailedWords = "Sync failed";

// Details on hover, beside the words above.
export const syncWaitingDetail = "Getting ready to copy the last 90 days of mail.";
export const needsReconnectDetail =
  "Google stopped accepting inbok's access. Reconnect it in Connected accounts.";
export const tokenUnreadableDetail =
  "inbok can no longer use the access it saved. Reconnect it in Connected accounts.";

// syncingDetail says how far a running full sync has copied.
export function syncingDetail(messagesSoFar: number): string {
  const messages = messagesSoFar === 1 ? "message" : "messages";
  return `Copying the last 90 days: ${messageCount(messagesSoFar)} ${messages} so far.`;
}
