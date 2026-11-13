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

// messageCount returns count as a row shows it, e.g. "1,284".
export function messageCount(count: number): string {
  return count.toLocaleString("en-US");
}

// How a row calls the account itself.
export const me = "me";

// A date on the day before today.
export const yesterday = "Yesterday";

// Who a row names when its newest messages carry no sender at all.
export const unknownSender = "Unknown sender";

// The list's end, once nothing older is left to load.
export const listEnd = "That's everything inbok has copied. Older mail stays in Gmail.";

// Read while a list's first page loads, and while it loads older ones.
export const listLoading = "Loading conversations...";
export const listLoadingMore = "Loading older conversations...";
