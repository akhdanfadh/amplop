import { onlineManager, type QueryClient } from "@tanstack/react-query";
import {
  BasicIndex,
  and,
  createCollection,
  eq,
  inArray,
  parseOrderByExpression,
  parseWhereExpression,
  type LoadSubsetOptions,
  type Ref,
} from "@tanstack/db";
import { queryCollectionOptions } from "@tanstack/query-db-collection";
import { listConversations } from "@/api/generated/inbok";
import type { PlaceCountOutput } from "@/api/generated/model/placeCount.zod";
import type { ConversationRowOutput } from "@/api/generated/model/conversationRow.zod";
import { listConversationsParamsLimitMax } from "@/api/generated/model/listConversationsParams.zod";
import { onAccountCacheClear } from "@/features/auth/session-cache";
import { isViewPlace, type Place, type ViewPlace } from "./places";

// Conversation is a list row as the API sends it, plus its date as a
// number: lists sort by dateMs, then id.
//
// NOTE: the date's text cannot be the order: Go's RFC 3339 drops trailing
// zero fractions ("...:00Z" beside "...:00.5Z"), so the strings do not sort
// by time (ADR031, TanStack DB gate, rule 6).
export type Conversation = ConversationRowOutput & { dateMs: number };

// toConversation returns row with its date as a number.
export function toConversation(row: ConversationRowOutput): Conversation {
  return { ...row, dateMs: Date.parse(row.date) };
}

// The row flag each view filters on; Starred also needs allMail.
const viewFlag = {
  inbox: "inbox",
  starred: "starred",
  sent: "sent",
  all: "allMail",
  spam: "spam",
  trash: "trash",
} as const satisfies Record<ViewPlace, keyof Conversation>;

type Flag = (typeof viewFlag)[ViewPlace];

// The view each flag stands for, viewFlag read backwards.
const flagView: Record<Flag, ViewPlace> = {
  inbox: "inbox",
  starred: "starred",
  sent: "sent",
  allMail: "all",
  spam: "spam",
  trash: "trash",
};

// isFlag reports whether name is a view's flag.
function isFlag(name: string): name is Flag {
  return Object.hasOwn(flagView, name);
}

// Test is one condition of a list's filter.
type Test = ReturnType<typeof eq>;

// placeWhere returns the filter of a list: the conversations in place, a
// view or a label's ID, and of account alone when given.
//
// DESIGN: a label is a test on labelIds, which TanStack DB evaluates as
// "contains" and loadConversations sends as place=<label id> (ADR031,
// TanStack DB gate, rule 8); so rows keep labelIds.
export function placeWhere(c: Ref<Conversation>, place: Place, account: number | undefined): Test {
  const tests: Test[] = [];
  if (!isViewPlace(place)) tests.push(inArray(place, c.labelIds));
  else if (place === "starred") tests.push(eq(c.starred, true), eq(c.allMail, true));
  else tests.push(eq(c[viewFlag[place]], true));
  if (account !== undefined) tests.push(eq(c.accountId, account));
  const [first, second, ...rest] = tests;
  return second === undefined ? first : and(first, second, ...rest);
}

// Scope is what one load asks the API for: a place, perhaps one account,
// and the dates it covers.
export type Scope = {
  place: Place;
  account?: number;
  // The newest date the load covers, and whether it is included.
  upper?: { dateMs: number; inclusive: boolean };
  // The oldest date the load covers, and whether it is included.
  lower?: { dateMs: number; inclusive: boolean };
  // The one date the load covers: the rest of a date's ties.
  dateMs?: number;
};

// parseScope returns the Scope of a load TanStack DB asks for: options'
// where and its cursor's whereFrom, as placeWhere and the list's order
// write them. It throws on anything else, which no list asks for.
export function parseScope(options: LoadSubsetOptions): Scope {
  const flags = new Set<Flag>();
  let label: string | undefined;
  let account: number | undefined;
  let dateMs: number | undefined;
  let upper: Scope["upper"];
  let lower: Scope["lower"];

  const bound = (field: string[], value: unknown, side: "upper" | "lower", inclusive: boolean) => {
    if (field.join(".") !== "dateMs") throw new Error(`Unexpected bound on ${field.join(".")}.`);
    const next = { dateMs: Number(value), inclusive };
    const current = side === "upper" ? upper : lower;
    // Two bounds on one side keep the narrower.
    const narrower =
      !current ||
      (side === "upper" ? next.dateMs < current.dateMs : next.dateMs > current.dateMs) ||
      (next.dateMs === current.dateMs && !inclusive);
    if (!narrower) return true;
    if (side === "upper") upper = next;
    else lower = next;
    return true;
  };
  const visit = (expression: LoadSubsetOptions["where"]) => {
    if (!expression) return;
    parseWhereExpression(expression, {
      handlers: {
        and: () => true,
        eq: (field: string[], value: unknown) => {
          const name = field.join(".");
          if (isFlag(name) && value === true) flags.add(name);
          else if (name === "accountId") account = Number(value);
          else if (name === "dateMs") dateMs = Number(value);
          else throw new Error(`Unexpected eq on ${name}.`);
          return true;
        },
        // inArray(label, c.labelIds): the row's labels contain label.
        in: (value: unknown, field: unknown) => {
          if (
            typeof value !== "string" ||
            !Array.isArray(field) ||
            field.join(".") !== "labelIds"
          ) {
            throw new Error("Unexpected in.");
          }
          label = value;
          return true;
        },
        lt: (field: string[], value: unknown) => bound(field, value, "upper", false),
        lte: (field: string[], value: unknown) => bound(field, value, "upper", true),
        gt: (field: string[], value: unknown) => bound(field, value, "lower", false),
        gte: (field: string[], value: unknown) => bound(field, value, "lower", true),
      },
      onUnknownOperator: (operator) => {
        throw new Error(`Unexpected operator ${operator}.`);
      },
    });
  };
  visit(options.where);
  visit(options.cursor?.whereFrom);

  // Starred is the starred conversations of All mail.
  if (flags.has("starred")) flags.delete("allMail");
  let place: Place;
  if (label !== undefined) {
    if (flags.size > 0) throw new Error("A label and a view in one load.");
    place = label;
  } else {
    const [flag] = flags;
    if (flags.size !== 1 || flag === undefined) throw new Error("No single place in a load.");
    place = flagView[flag];
  }
  return { place, account, upper, lower, dateMs };
}

// checkOrder throws unless options sort as every list does: newest first,
// then by ID.
function checkOrder(options: LoadSubsetOptions): void {
  if (!options.orderBy) return;
  const order = parseOrderByExpression(options.orderBy)
    .map((part) => `${part.field.join(".")} ${part.direction}`)
    .join(", ");
  if (order !== "dateMs desc, id desc" && order !== "dateMs desc") {
    throw new Error(`Unexpected order ${order}.`);
  }
}

// With a date as before, the highest ID takes in every conversation of
// that date: before and beforeId together mean "older than, or of that
// date with a lower ID". A date alone means older dates only.
const highestId = "ffffffff-ffff-ffff-ffff-ffffffffffff";

// How long a page's last date's ties stay ready for the load that asks for
// them next (TanStack DB asks for one date's ties right after a page).
const tiesKeptMs = 5_000;

// A date's ties a page found whole, for the load that asks for them next.
type Ties = { at: number; rows: Conversation[] };

// A position in a list: after the conversation of this date and ID, or
// after every one of its date without an ID.
type Cursor = { dateMs: number; id?: string };

// loadConversations returns the conversations a TanStack DB load asks for,
// reading the API's pages of at most 100 until it has them all. ties holds
// dates whose ties a page already read whole.
//
// DESIGN: TanStack DB loads an ordered window as a prefix, then pages
// after the last date (cursor.whereFrom), plus each page's last date's
// ties apart; each page asks for one row more than it needs, so the ties
// of its last date are known whole when the extra row is older, and that
// next load needs no request (ADR031, TanStack DB gate, rule 3).
async function loadConversations(
  options: LoadSubsetOptions,
  ties: Map<string, Ties>,
  signal: AbortSignal,
): Promise<Conversation[]> {
  checkOrder(options);
  const scope = parseScope(options);
  // NOTE: when a page fails, TanStack DB repairs the list by asking for
  // the whole place (its ordered loader's loadFullSource, retried with
  // backoff), hundreds of requests for a large Inbox. No list asks for
  // that otherwise, so it is refused; the list offers Try again instead.
  if (options.limit === undefined && scope.dateMs === undefined) throw new WholePlaceRefused();
  const tiesKey = (dateMs: number) => `${scope.place} ${scope.account ?? ""} ${dateMs}`;
  if (scope.dateMs !== undefined) {
    const found = ties.get(tiesKey(scope.dateMs));
    ties.delete(tiesKey(scope.dateMs));
    if (found && Date.now() - found.at < tiesKeptMs) return found.rows;
  }

  let before: Cursor | undefined;
  if (scope.dateMs !== undefined) before = { dateMs: scope.dateMs, id: highestId };
  else if (scope.upper) {
    const { dateMs, inclusive } = scope.upper;
    before = inclusive ? { dateMs, id: highestId } : { dateMs };
  }
  // A cursor already skips the rows before it; without one, an offset
  // counts from the newest.
  const skip = options.cursor ? 0 : (options.offset ?? 0);
  const want = options.limit ?? Infinity;
  const peek = scope.dateMs === undefined && Number.isFinite(want);

  const rows: Conversation[] = [];
  let skipped = 0;
  let extra: Conversation | undefined;
  for (;;) {
    signal.throwIfAborted();
    const missing = Number.isFinite(want) ? want - rows.length + (skip - skipped) : Infinity;
    const limit = Math.min(listConversationsParamsLimitMax, missing + (peek ? 1 : 0));
    // oxlint-disable-next-line eslint/no-await-in-loop -- each page starts where the last ended.
    const page = await listConversations(
      {
        place: scope.place,
        account: scope.account,
        before: before && new Date(before.dateMs).toISOString(),
        beforeId: before?.id,
        limit,
      },
      { signal },
    );
    const conversations = page.data.conversations.map(toConversation);
    let done = conversations.length < limit;
    for (const conversation of conversations) {
      if (outside(conversation, scope)) {
        done = true;
        break;
      }
      if (skipped < skip) {
        skipped++;
        continue;
      }
      if (rows.length >= want) {
        extra = conversation;
        done = true;
        break;
      }
      rows.push(conversation);
    }
    const last = conversations.at(-1);
    if (done || rows.length >= want || !last) break;
    before = { dateMs: last.dateMs, id: last.id };
  }

  const last = rows.at(-1);
  if (peek && last && (!extra || extra.dateMs < last.dateMs)) {
    ties.set(tiesKey(last.dateMs), {
      at: Date.now(),
      rows: rows.filter((row) => row.dateMs === last.dateMs),
    });
  }
  return rows;
}

// outside reports whether conversation, read in order, is past scope's
// oldest date.
function outside(conversation: Conversation, scope: Scope): boolean {
  if (scope.dateMs !== undefined) return conversation.dateMs < scope.dateMs;
  if (!scope.lower) return false;
  const { dateMs, inclusive } = scope.lower;
  return conversation.dateMs < dateMs || (!inclusive && conversation.dateMs === dateMs);
}

// WholePlaceRefused is the error of a load that asked for a whole place,
// which loadConversations refuses.
export class WholePlaceRefused extends Error {
  constructor() {
    super("A list asked for a whole place, which is never read at once.");
    this.name = "WholePlaceRefused";
  }
}

// The query key under which the conversations collection keeps its loads.
const conversationsKey = ["mail", "conversations"];

// How often each open list reads its newest page again.
const newestEveryMs = 30_000;

// PlaceCount is one account's place with its unread count, as List Places
// gives it: a view, or a label with its own name and its parent label.
export type PlaceCount = PlaceCountOutput;

// createConversations returns the conversations collection over client:
// every list's rows, loaded as lists ask for them; and stop, which ends
// its refresh. number tells it from the collections before it.
//
// DESIGN: no refetchInterval: it reads every loaded page again, 200
// requests at row 5,000; inbok's own timer reads only each open list's
// newest page (ADR031, TanStack DB gate, rule 2).
function createConversations(client: QueryClient, number: number) {
  const ties = new Map<string, Ties>();
  const conversations = createCollection(
    queryCollectionOptions<Conversation>({
      id: `conversations-${number}`,
      queryKey: conversationsKey,
      queryClient: client,
      syncMode: "on-demand",
      getKey: (conversation) => conversation.id,
      defaultIndexType: BasicIndex,
      refetchInterval: false,
      refetchOnWindowFocus: false,
      // Back online, every loaded page would be read again (rule 2); the
      // newest pages are, below.
      refetchOnReconnect: false,
      staleTime: 0,
      queryFn: async ({ meta, signal }) => {
        const options = (meta as { loadSubsetOptions?: LoadSubsetOptions } | undefined)
          ?.loadSubsetOptions;
        return loadConversations(options ?? {}, ties, signal);
      },
    }),
  );
  conversations.createIndex((conversation) => conversation.dateMs);

  const refresh = () => void refreshNewest(client);
  // A hidden tab skips its refreshes; showing it again refreshes at once.
  const timer = setInterval(() => {
    if (document.visibilityState === "visible") refresh();
  }, newestEveryMs);
  const onVisible = () => {
    if (document.visibilityState === "visible") refresh();
  };
  document.addEventListener("visibilitychange", onVisible);
  const stopOnline = onlineManager.subscribe((online) => {
    if (online) refresh();
  });
  const stop = () => {
    clearInterval(timer);
    document.removeEventListener("visibilitychange", onVisible);
    stopOnline();
  };
  return { conversations, stop };
}

export type MailData = ReturnType<typeof createConversations>;

// refreshNewest reads the newest page of each list open on client again,
// and settles when they have; older pages stay as they are.
export async function refreshNewest(client: QueryClient): Promise<void> {
  await client.refetchQueries({
    queryKey: conversationsKey,
    type: "active",
    // A list's newest page: a load with a limit, from the newest.
    predicate: (query) => {
      const options = (query.meta as { loadSubsetOptions?: LoadSubsetOptions } | undefined)
        ?.loadSubsetOptions;
      return !!options && !options.cursor && !options.offset && options.limit !== undefined;
    },
  });
}

const mailByClient = new WeakMap<QueryClient, MailData>();
let created = 0;

// currentMail returns client's mail collections if any list has started
// them, without starting them, e.g. for a lookup after sign-out.
export function currentMail(client: QueryClient): MailData | undefined {
  return mailByClient.get(client);
}

// mailData returns client's mail collections, creating them on first use.
export function mailData(client: QueryClient): MailData {
  let data = mailByClient.get(client);
  if (!data) {
    created++;
    data = createConversations(client, created);
    mailByClient.set(client, data);
  }
  return data;
}

// restartMail drops client's mail collections and their loads, so the
// next mailData starts anew, e.g. after a list's first page failed, which
// leaves its collection failed too.
export function restartMail(client: QueryClient): void {
  dropMail(client);
  client.removeQueries({ queryKey: conversationsKey });
}

// dropMail ends client's mail collections, if any: their refresh, and the
// lists still open on them, e.g. as the session ends on a mail page.
function dropMail(client: QueryClient): void {
  const data = mailByClient.get(client);
  if (!data) return;
  mailByClient.delete(client);
  data.stop();
  void data.conversations.cleanup({ dependents: true });
}

// One member's mail ends with their session (session-cache.ts): the next
// mailData starts empty.
// The module lives as long as the page, so it never stops listening.
void onAccountCacheClear(dropMail);
