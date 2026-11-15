import { and, eq, inArray, type Ref } from "@tanstack/db";
import type { ConversationActionBodyOutput } from "@/api/generated/model/conversationActionBody.zod";
import type { Conversation } from "./conversations";
import { isViewPlace, viewPlaces, type Place, type ViewPlace } from "./places";

// Action is what the API can do to conversations, e.g. "star".
export type Action = ConversationActionBodyOutput["action"];

// Request is what the web app asks of conversations: one action.
export type Request = { action: Action; ids: string[] };

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
// its action's guess.
export function guess(conversation: Conversation, request: Request): Conversation {
  return { ...conversation, ...guesses[request.action] };
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
