import { uncertainOutcome, userMessage } from "@/api/problem";
import { formatCount } from "@/lib/count";
import type { ApiError } from "@/api/transport";
import {
  accountNotFoundType,
  needsReconnectType,
  throttledType,
} from "@/features/connected-accounts/text";
import type { ViewPlace } from "./places";

const notFound = 404;
const conflict = 409;
const tooManyRequests = 429;

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

// subjectText shows subject, or says the message has none.
export function subjectText(subject: string): string {
  return subject.trim() || noSubject;
}

// inboxChip is the chip a row outside the Inbox shows while its
// conversation is in the Inbox, as Gmail's list marks it.
export const inboxChip = "Inbox";

// rowName returns what a screen reader calls a list row: whether it is
// unread, who, how many messages when more than one, its subject and date,
// whether its list marks it in the Inbox (inbox) and whether it is
// starred, and its account when the list mixes accounts.
export function rowName(row: {
  who: string;
  count: number | undefined;
  subject: string;
  date: string;
  unread: boolean;
  inbox: boolean;
  starred: boolean;
  account: string | undefined;
}): string {
  return [
    row.unread && "Unread",
    row.who,
    row.count !== undefined && `${messageCount(row.count)} messages`,
    row.subject,
    row.date,
    row.inbox && `In ${inboxChip}`,
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

// Why the API refused to change a conversation, with the fix.
export const refusedText = {
  "not-found": "It's no longer in Gmail.",
  "needs-reconnect": "Reconnect its account in Settings, then try again.",
  failed: "Gmail didn't answer. Try again.",
} as const;

// findItIn returns where the conversation went, e.g. "Trash", which the
// Undo bar tells screen readers.
export function findItIn(where: string): string {
  return `Find it in ${where}.`;
}

export const undoName = "Undo";
// The Undo bar's name as a landmark, which F6 reaches.
export const undoBarName = "Undo";
export const closeName = "Close";
export const undoFailed = "Couldn't undo";

// undoTooltip is Undo's tooltip: its name and the keys that undo, as the
// platform's keyboard writes them (DESIGN.md "Wording").
export function undoTooltip(keys: string): string {
  return `${undoName} (${keys})`;
}

// undone says, for screen readers, that Undo put the conversation of
// subject back.
export function undone(subject: string): string {
  return `Undone: ${subject}.`;
}

// What a conversation's toolbar's More menu is called, and its link.
export const moreName = "More actions";
export const openInGmail = "Open in Gmail";
export const opensInNewTab = "(opens in a new tab)";

// Who a row names when its newest messages carry no sender at all.
export const unknownSender = "Unknown sender";

// The list's end, once nothing older is left to load.
export const listEnd = "That's everything inbok has copied. Older mail stays in Gmail.";

// Read while a list's first page loads, and while it loads older ones.
export const listLoading = "Loading conversations...";
export const listLoadingMore = "Loading older conversations...";

// The phone's way from a conversation back to its list.
export const backToList = "Back to list";

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

// Check for new mail's name, as its button and tooltip say it.
export const checkMailName = "Check for new mail";
export const checkFailedTitle = "Couldn't check for new mail";
// Said when the browser is offline, so no check can start.
export const offlineCheck = "You're offline, so inbok can't check for new mail.";
// Said when the member has no account to check.
export const noAccountToCheck = "Connect an account in Connected accounts to get its mail.";
// Said when the accounts have not been read, or could not be.
export const accountsNotRead =
  "inbok couldn't read your accounts, so it checked nothing. Try again.";

// CheckRefusal is why Check for new mail could not check one account: it
// is no longer connected, it needs reconnecting, the outcome is unknown,
// or it failed otherwise, with what to say.
export type CheckRefusal =
  | { kind: "gone" | "reconnect" | "uncertain" }
  | { kind: "failed"; message: string };

// checkRefusal returns why err refused a check, or undefined for a refusal
// that means a check is running anyway: one started moments ago.
export function checkRefusal(err: ApiError): CheckRefusal | undefined {
  const type = err.problem?.type;
  if (err.status === tooManyRequests && type === throttledType) return undefined;
  if (err.status === notFound && type === accountNotFoundType) return { kind: "gone" };
  if (err.status === conflict && type === needsReconnectType) return { kind: "reconnect" };
  // Unknown, not failed: the check may have started.
  if (uncertainOutcome(err)) return { kind: "uncertain" };
  return { kind: "failed", message: userMessage(err) };
}

// Names in a sentence: "A, B, and C".
const names = new Intl.ListFormat("en-US", { type: "conjunction" });

// checkFailures says why the accounts in refused, each named with its
// refusal, could not be checked: one sentence per reason, naming every
// account it holds, so many refusals stay short.
export function checkFailures(refused: readonly { name: string; refusal: CheckRefusal }[]): string {
  const of = (kind: CheckRefusal["kind"]) =>
    refused.filter(({ refusal }) => refusal.kind === kind).map(({ name }) => name);
  const sentences: string[] = [];
  const gone = of("gone");
  if (gone.length === 1) sentences.push(`${gone[0]} is no longer connected.`);
  if (gone.length > 1)
    sentences.push(`${gone.length} accounts are no longer connected: ${names.format(gone)}.`);
  const reconnect = of("reconnect");
  if (reconnect.length === 1)
    sentences.push(`Reconnect ${reconnect[0]} in Connected accounts to get its mail.`);
  if (reconnect.length > 1)
    sentences.push(
      `Reconnect ${reconnect.length} accounts in Connected accounts to get their mail: ${names.format(reconnect)}.`,
    );
  const uncertain = of("uncertain");
  if (uncertain.length === 1)
    sentences.push(
      `Couldn't tell whether inbok checked ${uncertain[0]}. Its mail still arrives within a minute.`,
    );
  if (uncertain.length > 1)
    sentences.push(
      `Couldn't tell whether inbok checked ${uncertain.length} accounts: ${names.format(uncertain)}. Their mail still arrives within a minute.`,
    );
  for (const { name, refusal } of refused) {
    if (refusal.kind === "failed")
      sentences.push(`inbok couldn't check ${name}. ${refusal.message}`);
  }
  return sentences.join(" ");
}

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

// Search (DESIGN.md "Search").

// The search box's name, and what it says while empty: the places it
// searches, e.g. "Search Receipts", or mail for every place but Spam and
// Trash.
export const searchName = "Search mail";
export function searchPlaceholder(place: string | undefined): string {
  return place === undefined ? searchName : `Search ${place}`;
}
export const clearSearch = "Clear search";
// The top bar's button that turns it into the box, and the one that
// leaves the search again.
export const openSearch = "Search";
export const closeSearch = "Close search";
// The results page's heading, which screen readers hear.
export const resultsHeading = "Search results";
// The results list's name for a screen reader.
export const resultsName = "Results";

// The filters as one sentence: "Searching all mail in every account, from
// anyone, any time, with or without attachments."
export const searchingWord = "Searching";
export const inWord = "in";
export const fromWord = "from";
export const allMailPhrase = "all mail";
export const everyAccount = "every account";
export const fromAnyone = "anyone";
export const anyTime = "any time";
export const anyAttachment = "with or without attachments";
export const withAttachment = "with an attachment";

// The menus the sentence's words open.
export const placesMenuName = "Search in";
export const viewsGroupName = "Views";
export const accountMenuName = "Account";
export const attachmentMenuName = "Attachments";
export const dateMenuName = "Date";
export const fromName = "From";
export const sendersName = "Senders";
export function fromHelp(most: number): string {
  return `Type an address and press Enter. Any of them matches, up to ${most}.`;
}
export const doneName = "Done";
export const clearName = "Clear";

// placesPhrase names places in the sentence: up to three, then how many
// more.
export function placesPhrase(places: readonly string[]): string {
  if (places.length === 0) return allMailPhrase;
  if (places.length === 1) return places[0];
  if (places.length <= 3) return `${places.slice(0, -1).join(", ")} and ${places.at(-1)}`;
  return `${places.slice(0, 2).join(", ")} and ${messageCount(places.length - 2)} more`;
}

// fromPhrase names the senders in the sentence: the first, then how many
// more.
export function fromPhrase(from: readonly string[]): string {
  if (from.length === 0) return fromAnyone;
  if (from.length === 1) return from[0];
  return `${from[0]} and ${messageCount(from.length - 1)} more`;
}

// The date choices relative to today, as the date menu lists them and as
// the sentence says them.
export const datePresetNames = {
  "7d": "Past 7 days",
  "30d": "Past 30 days",
  "1y": "Past year",
  older: "Older than a year",
} as const;
export const anyTimeName = "Any time";
export const datePresetPhrases = {
  "7d": "in the past 7 days",
  "30d": "in the past 30 days",
  "1y": "in the past year",
  older: "older than a year",
} as const;

// rangePhrase says a chosen range in the sentence, as days inbok writes:
// "since 2 Nov 2026 and before 14 Nov 2026".
export function rangePhrase(since: string | undefined, before: string | undefined): string {
  if (since && before) return `since ${since} and before ${before}`;
  if (since) return `since ${since}`;
  return before ? `before ${before}` : anyTime;
}

// The date chooser's own words.
export const sinceName = "Since";
export const beforeName = "Before";
export const rangeHelp = "Before is not included. Leave either empty.";
export const rangeBackwards = "Before must come after Since.";
export const rangeFuture = "Pick days up to today.";
export const rangePartly = "Finish typing the day, or clear it.";
// placesLimit says why the places menu takes no more.
export function placesLimit(most: number): string {
  return `Search up to ${messageCount(most)} places at once.`;
}
export const applyName = "Apply";
export function pickDayName(field: string): string {
  return `Pick the ${field} day`;
}
export function clearFieldName(field: string): string {
  return `Clear ${field}`;
}

// What Spam and Trash searches leave out, and the way to add them.
export const spamTrashLeftOut = "Spam and Trash are not searched.";
export const searchThemToo = "Search them too";

// resultsCount says how many conversations a search found, as the API
// counts up to most: "1 conversation", "1,000 or more conversations".
export function resultsCount(total: number, most: number): string {
  if (total >= most) return `${messageCount(most)} or more conversations`;
  return total === 1 ? "1 conversation" : `${messageCount(total)} conversations`;
}
export const resultsEnd = "That's every match.";
export const resultsLoading = "Searching...";
export const resultsLoadingMore = "Loading more results...";
export const olderResultsFailed = "Couldn't load more results.";

// wordsTooLong says a search's words are longer than the API takes.
export function wordsTooLong(most: number): string {
  return `Search with ${messageCount(most)} characters or fewer.`;
}

// What the results page says before there is anything to search.
export const searchPrompt = "Type 2 or more characters, or choose a filter.";

// noMatches is the title of a search that found nothing: its words, or
// its filters when it has none.
export function noMatches(words: string | undefined): string {
  return words === undefined ? "Nothing matches these filters" : `Nothing matches "${words}"`;
}
// stillCopying says why a search may find more later: account, or some
// account when undefined, is still copying its mail.
export function stillCopying(account: string | undefined): string {
  return account === undefined
    ? "Some accounts are still syncing, so some of their mail can't be found yet."
    : `${account} is still syncing, so some of its mail can't be found yet.`;
}
export const clearFilters = "Clear filters";
export const searchSpamTrashToo = "Search Spam and Trash too";

// Why a search failed.
export const searchBusy = "Search isn't answering right now. Try again in a moment.";
export const searchOffline = "Couldn't search. Check your connection, then try again.";
export const searchFailed = "Couldn't search your mail.";
