import { and, eq, inArray, type Ref } from "@tanstack/db";
import type { ListedAccountOutput } from "@/api/generated/model/listedAccount.zod";
import type { ConversationActionBodyOutput } from "@/api/generated/model/conversationActionBody.zod";
import type { ConversationChangeOutput } from "@/api/generated/model/conversationChange.zod";
import type { Conversation } from "./conversations";
import { isViewPlace, viewPlaces, type Place, type ViewPlace } from "./places";

// Action is what the API can do to conversations, e.g. "star", by their
// provider's own rules.
export type Action = ConversationActionBodyOutput["action"];

// ConversationChange is a change of conversations as it is, e.g. an
// action's undo, which Change Conversations applies.
export type ConversationChange = ConversationChangeOutput;

// Request is what the web app asks of conversations: one action, or
// changes, each to its own.
export type Request = { action: Action; ids: string[] } | { changes: ConversationChange[] };

// Act is what the web app does to conversations: an action, by their
// provider's rules, or changes as they are, e.g. a move to a label.
export type Act = Action | { changes: readonly ConversationChange[] };

// requestOf returns the request of act for ids: the action on them, or
// act's changes cut to them, each change that names none left out.
export function requestOf(act: Act, ids: readonly string[]): Request {
  if (typeof act === "string") return { action: act, ids: [...ids] };
  const named = new Set(ids);
  const changes = act.changes
    .map((change) => ({ ...change, ids: change.ids.filter((id) => named.has(id)) }))
    .filter((change) => change.ids.length > 0);
  return { changes };
}

// idsOf returns the conversations request reaches, each once.
export function idsOf(request: Request): string[] {
  return "action" in request ? request.ids : request.changes.flatMap((change) => change.ids);
}

// The flags a conversation has, all of them, in each view: Starred is the
// starred conversations of All mail, as List Places counts it. Each list
// filters by it (placeWhere), and each count guess reads it (placesOf),
// so a count and its list agree.
export const viewFlags = {
  inbox: ["inbox"],
  starred: ["starred", "allMail"],
  sent: ["sent"],
  all: ["allMail"],
  spam: ["spam"],
  trash: ["trash"],
} as const satisfies Record<ViewPlace, readonly (keyof Conversation)[]>;

// Flag is a flag a view filters on.
export type Flag = (typeof viewFlags)[ViewPlace][number];

// Test is one condition of a list's filter.
type Test = ReturnType<typeof eq>;

// placeWhere returns the filter of a list: the conversations in place, a
// view or a label's ID, and of account alone when given.
//
// DESIGN: a label is a test on labelIds, which TanStack DB evaluates as
// "contains" and loadConversations sends as place=<label id> (ADR031,
// TanStack DB gate, rule 8); so rows keep labelIds.
export function placeWhere(c: Ref<Conversation>, place: Place, account: number | undefined): Test {
  const tests: Test[] = isViewPlace(place)
    ? viewFlags[place].map((flag) => eq(c[flag], true))
    : [inArray(place, c.labelIds)];
  if (account !== undefined) tests.push(eq(c.accountId, account));
  const [first, second, ...rest] = tests;
  return second === undefined ? first : and(first, second, ...rest);
}

// placesOf returns the places conversation is in, as the lists filter:
// its views, then its labels.
export function placesOf(conversation: Conversation): Place[] {
  const views = viewPlaces.filter((view) => viewFlags[view].every((flag) => conversation[flag]));
  return [...views, ...conversation.labelIds];
}

// The fields each action sets on a conversation, before the API answers.
// Trash and Spam also leave every other place but the Spam or Trash they
// share, as the server's rows show a conversation only there, so a label
// or Sent list drops it at once (ADR031, TanStack DB gate, rule 8).
const guesses: Readonly<Record<Action, Partial<Conversation>>> = {
  read: { unread: false },
  unread: { unread: true },
  archive: { inbox: false },
  unarchive: { inbox: true },
  star: { starred: true },
  unstar: { starred: false },
  trash: { trash: true, inbox: false, allMail: false, sent: false, spam: false, labelIds: [] },
  untrash: { trash: false, allMail: true, inbox: true },
  spam: { spam: true, inbox: false, allMail: false, sent: false, labelIds: [] },
  unspam: { spam: false, inbox: true, allMail: true },
};

// The flags a guess can set.
const flags = ["unread", "inbox", "starred", "trash", "allMail", "sent", "spam"] as const;

// guess returns conversation as request leaves it, before the API answers:
// an action's guess, or its own change as it is.
export function guess(conversation: Conversation, request: Request): Conversation {
  if ("action" in request) return { ...conversation, ...guesses[request.action] };
  const change = request.changes.find((c) => c.ids.includes(conversation.id));
  return change ? changed(conversation, change) : conversation;
}

// changed returns conversation with change applied as it is. All mail
// follows Spam and Trash: a conversation out of both is in All mail.
function changed(conversation: Conversation, change: ConversationChange): Conversation {
  const add = change.add ?? [];
  const remove = change.remove ?? [];
  const has = (view: "inbox" | "spam" | "trash") =>
    !remove.includes(view) && (add.includes(view) || conversation[view]);
  const [inbox, spam, trash] = [has("inbox"), has("spam"), has("trash")];
  const labels = add.filter((place) => !isViewPlace(place));
  const allMail =
    spam !== conversation.spam || trash !== conversation.trash
      ? !spam && !trash
      : conversation.allMail;
  return {
    ...conversation,
    inbox,
    spam,
    trash,
    allMail,
    // A row wholly in Spam or Trash lists no labels, as the server's rows
    // and the Delete and Report spam guesses show it.
    labelIds: allMail
      ? [
          ...conversation.labelIds.filter((label) => !remove.includes(label)),
          ...labels.filter((label) => !conversation.labelIds.includes(label)),
        ]
      : [],
    starred: change.starred ?? conversation.starred,
    unread: change.unread ?? conversation.unread,
  };
}

// guessedFields returns the fields of after that differ from before, as a
// guess sets them on a row.
export function guessedFields(before: Conversation, after: Conversation): Partial<Conversation> {
  const fields: Partial<Conversation> = {};
  for (const flag of flags) if (after[flag] !== before[flag]) fields[flag] = after[flag];
  const sameLabels =
    after.labelIds.length === before.labelIds.length &&
    after.labelIds.every((label) => before.labelIds.includes(label));
  if (!sameLabels) fields.labelIds = after.labelIds;
  return fields;
}

// guessChanges reports whether request's guess would change conversation.
export function guessChanges(conversation: Conversation, request: Request): boolean {
  return Object.keys(guessedFields(conversation, guess(conversation, request))).length > 0;
}

// UnreadChange is how much one account's place's unread count changes.
export type UnreadChange = { accountId: number; place: Place; by: number };

// countChanges returns the unread counts that change as a conversation
// goes from before to after: one more in each place it is unread in after
// and not before, one fewer in each the other way round.
//
// DESIGN: a guess, as the list shows it, bold in every place it is in.
// The server counts a conversation in a place when one of its messages
// there is unread, which a row cannot tell; the action's answer brings
// the counts as the server has them.
export function countChanges(before: Conversation, after: Conversation): UnreadChange[] {
  const was = new Set(before.unread ? placesOf(before) : []);
  const is = new Set(after.unread ? placesOf(after) : []);
  const { accountId } = after;
  return [
    ...[...is].filter((place) => !was.has(place)).map((place) => ({ accountId, place, by: 1 })),
    ...[...was].filter((place) => !is.has(place)).map((place) => ({ accountId, place, by: -1 })),
  ];
}

// Changers is the accounts, by ID, whose conversations take actions:
// those whose allowedActions list change-mail, as the API offers them
// (AGENTS.md, "API contract").
export type Changers = ReadonlySet<number>;

// changersOf returns the Changers of accounts, as the API listed them; none
// until they are read.
export function changersOf(
  accounts: readonly Pick<ListedAccountOutput, "id" | "allowedActions">[] | undefined,
): Changers {
  return new Set(
    (accounts ?? []).filter((a) => a.allowedActions.includes("change-mail")).map((a) => a.id),
  );
}

// changeable returns the conversations, of conversations, whose account
// takes actions (changers), in their order.
export function changeable(
  conversations: readonly Conversation[],
  changers: Changers,
): Conversation[] {
  return conversations.filter((conversation) => changers.has(conversation.accountId));
}

// The order actions are offered in: the state's move first, then Delete,
// Report spam, the marks, and the star, as the toolbar shows them.
const actionOrder: readonly Action[] = [
  "archive",
  "unarchive",
  "unspam",
  "untrash",
  "trash",
  "spam",
  "read",
  "unread",
  "star",
  "unstar",
];

// actionsFor returns the actions conversations offer in list, a place's
// list, in the toolbar's order: each action any of them offers, so
// several mixed conversations offer both of a pair, e.g. Archive and Move
// to Inbox. One conversation offers its state's: a Spam or Trash list
// offers its own, so a conversation with one message there can leave it;
// elsewhere its flags decide: in the inbox, archived, or only in Spam or
// Trash, as those lists. No Delete forever: it cannot be undone. A
// conversation whose account takes no actions (changers) offers none.
export function actionsFor(
  conversations: readonly Conversation[],
  list: Place,
  changers: Changers,
): Action[] {
  const offered = new Set(
    changeable(conversations, changers).flatMap((conversation) => actionsOf(conversation, list)),
  );
  return actionOrder.filter((action) => offered.has(action));
}

// targetsOf returns the conversations, of conversations, that offer action
// in list, in their order: the ones it changes, of accounts that take
// actions (changers).
export function targetsOf(
  conversations: readonly Conversation[],
  action: Action,
  list: Place,
  changers: Changers,
): Conversation[] {
  return changeable(conversations, changers).filter((conversation) =>
    actionsOf(conversation, list).includes(action),
  );
}

// actionsOf returns the actions conversation offers in list, as
// actionsFor says.
function actionsOf(conversation: Conversation, list: Place): Action[] {
  const mark: Action = conversation.unread ? "read" : "unread";
  const star: Action = conversation.starred ? "unstar" : "star";
  const onlyIn = (view: "spam" | "trash") => conversation[view] && !conversation.allMail;
  if (list === "spam" || (list !== "trash" && onlyIn("spam")))
    return ["unspam", "trash", mark, star];
  if (list === "trash" || onlyIn("trash")) return ["untrash", mark, star];
  if (conversation.inbox) return ["archive", "trash", "spam", mark, star];
  return ["unarchive", "trash", "spam", mark, star];
}

// leavesList reports whether act takes conversation out of the list it
// was opened from, which keeps conversations in any of places, of account
// alone (undefined for every account's), as its guess shows it; Mark
// unread always does, so the member comes back to it unread. A place's
// list has one place; a search's, those it searches.
export function leavesList(
  conversation: Conversation,
  act: Act,
  places: readonly Place[],
  account: number | undefined,
): boolean {
  if (act === "unread") return true;
  return inList(conversation, places, account) && !staysInList(conversation, act, places, account);
}

// staysInList reports whether conversation is in the list of places, of
// account alone when given, once act's guess shows.
export function staysInList(
  conversation: Conversation,
  act: Act,
  places: readonly Place[],
  account: number | undefined,
): boolean {
  return inList(guess(conversation, requestOf(act, [conversation.id])), places, account);
}

// inList reports whether conversation is in any of places, and of account
// alone when given.
export function inList(
  conversation: Conversation,
  places: readonly Place[],
  account: number | undefined,
): boolean {
  const own = placesOf(conversation);
  return (
    places.some((place) => own.includes(place)) &&
    (account === undefined || conversation.accountId === account)
  );
}
