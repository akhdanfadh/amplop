import { onlineManager, type QueryClient } from "@tanstack/react-query";
import {
  BasicIndex,
  createCollection,
  parseOrderByExpression,
  parseWhereExpression,
  type LoadSubsetOptions,
} from "@tanstack/db";
import { queryCollectionOptions } from "@tanstack/query-db-collection";
import {
  getGetConversationQueryOptions,
  getListConnectedAccountsQueryKey,
  getListPlacesQueryKey,
  listConversations,
  listPlaces,
} from "@/api/generated/inbok";
import type { PlaceCountOutput } from "@/api/generated/model/placeCount.zod";
import type { ResponsePlacesOutput } from "@/api/generated/model/responsePlaces.zod";
import type { ConversationRowOutput } from "@/api/generated/model/conversationRow.zod";
import { listConversationsParamsLimitMax } from "@/api/generated/model/listConversationsParams.zod";
import { ApiError } from "@/api/transport";
import { onAccountCacheClear } from "@/features/auth/session-cache";
import { viewPlaces, type Place } from "./places";
import { viewFlags, type Flag } from "./rules";

// Conversation is a list row as the API sends it, plus its date as a
// number: lists sort by dateMs, then id.
//
// NOTE: the date's text cannot be the order: Go's RFC 3339 drops trailing
// zero fractions ("...:00Z" beside "...:00.5Z"), so the strings do not sort
// by time.
export type Conversation = ConversationRowOutput & { dateMs: number };

// toConversation returns row with its date as a number.
export function toConversation(row: ConversationRowOutput): Conversation {
  return { ...row, dateMs: Date.parse(row.date) };
}

const notFound = 404;

// The flags the views filter on.
const viewFlagNames = new Set<string>(Object.values(viewFlags).flat());

// isFlag reports whether name is a view's flag.
function isFlag(name: string): name is Flag {
  return viewFlagNames.has(name);
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

  let place: Place;
  if (label !== undefined) {
    if (flags.size > 0) throw new Error("A label and a view in one load.");
    place = label;
  } else {
    // The view whose flags the load tests, all of them and no other.
    const view = viewPlaces.find(
      (name) =>
        viewFlags[name].length === flags.size && viewFlags[name].every((flag) => flags.has(flag)),
    );
    if (view === undefined) throw new Error("No single place in a load.");
    place = view;
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
// next load needs no request.
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

// idOf returns the conversation ID a load asks for alone, as the reading
// pane's query by ID writes it, or undefined for any other load.
export function idOf(options: LoadSubsetOptions): string | undefined {
  if (options.cursor) return undefined;
  const id = parseWhereExpression<string | null>(options.where, {
    handlers: {
      eq: (field: string[], value: unknown) =>
        field.join(".") === "id" && typeof value === "string" ? value : null,
    },
    onUnknownOperator: () => null,
  });
  return id ?? undefined;
}

// idsOf returns the conversation IDs a load asks for together, as a
// search's results write them (inArray(c.id, ids)), or undefined for any
// other load.
export function idsOf(options: LoadSubsetOptions): string[] | undefined {
  if (options.cursor) return undefined;
  const ids = parseWhereExpression<string[] | null>(options.where, {
    handlers: {
      in: (field: unknown, values: unknown) =>
        Array.isArray(field) &&
        field.join(".") === "id" &&
        Array.isArray(values) &&
        values.every((value) => typeof value === "string")
          ? values
          : null,
    },
    onUnknownOperator: () => null,
  });
  return ids ?? undefined;
}

// Each collection's rows a search found, by ID, for its results' load.
const foundRows = new WeakMap<Conversations, Map<string, Conversation>>();

// holdFound keeps rows, the conversations the open search found, for the
// load of its results from conversations (idsOf), which reads no API: the
// search already did. They replace an earlier search's.
export function holdFound(conversations: Conversations, rows: readonly Conversation[]): void {
  foundRows.set(conversations, new Map(rows.map((row) => [row.id, row])));
}

// loadFound returns the rows of ids, each the collection's confirmed row,
// held, when it has one, e.g. a list's or as an action's answer left it,
// else as the search found it (holdFound).
//
// DESIGN: a search's results join the collection as the reading pane's row
// does, a load the results' live query owns, so an action, Mark read
// included, shows on them at once and every list agrees; the load ends
// with the results, and rows no list holds leave with it. A direct write
// would stay after the results end, unowned, and a list reaching it later
// would keep it even once the server no longer listed it there.
function loadFound(
  ids: readonly string[],
  found: Map<string, Conversation> | undefined,
  held: ((id: string) => Conversation | undefined) | undefined,
): Conversation[] {
  return ids.flatMap((id) => held?.(id) ?? found?.get(id) ?? []);
}

// loadOne returns conversation id as Get Conversation reads it through
// client's cache, so the reading pane's own read of its messages serves;
// none once the API says it is gone, and held, the collection's confirmed
// row, if any, when it cannot be read.
//
// DESIGN: a fetch, not the cache's last answer: an answer read minutes
// ago, e.g. as the member's pointer rested on the row, would overwrite the
// list's newer row. Joining the route's read while it is still in flight,
// it costs no request. A load's rows are the whole of its subset, and one
// with none gives the row up, which removes it unless a list holds it
// too, e.g. a conversation opened from a link: so a failed read keeps the
// row, rather than giving the collection the error, which every list on
// it would show; the pane says why from its own read, with Try again.
async function loadOne(
  client: QueryClient,
  id: string,
  held: Conversation | undefined,
): Promise<Conversation[]> {
  try {
    const detail = await client.query(getGetConversationQueryOptions(id));
    return [toConversation(detail.data.conversation)];
  } catch (err) {
    if (err instanceof ApiError && err.status === notFound) return [];
    return held ? [held] : [];
  }
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

// How often each open list reads its newest page again, and the places
// their unread counts.
const newestEveryMs = 30_000;
// How often mail is read again while a Check for new mail runs, and for
// how long after it starts: a check that finds a few messages ends within
// seconds, so its mail and counts show (the same as Connected accounts'
// Check now).
export const checkingEveryMs = 2_000;
export const checkingForMs = 30_000;

// PlaceCount is one account's place with its unread count, as List Places
// gives it: a view, or a label with its own name and its parent label.
export type PlaceCount = PlaceCountOutput;

// placeKey returns the key of an account's place in the places collection.
export function placeKey(accountId: number, place: string): string {
  return `${accountId} ${place}`;
}

// createConversations returns a conversations collection over client:
// every list's rows, loaded as lists ask for them. id tells it from the
// collections before it.
//
// DESIGN: no refetchInterval: it reads every loaded page again, 200
// requests at row 5,000; inbok's own timer (createMail) reads only each
// open list's newest page.
function createConversations(client: QueryClient, id: string) {
  const ties = new Map<string, Ties>();
  // Reads the collection's confirmed row by ID, and the rows searches
  // found, once it exists.
  const rows: {
    held?: (id: string) => Conversation | undefined;
    found?: () => Map<string, Conversation> | undefined;
  } = {};
  const conversations = createCollection(
    queryCollectionOptions<Conversation>({
      id,
      queryKey: conversationsKey,
      queryClient: client,
      syncMode: "on-demand",
      getKey: (conversation) => conversation.id,
      defaultIndexType: BasicIndex,
      refetchInterval: false,
      refetchOnWindowFocus: false,
      // Back online, every loaded page would be read again; the newest pages
      // are, below.
      refetchOnReconnect: false,
      staleTime: 0,
      queryFn: async ({ meta, signal }) => {
        const options =
          (meta as { loadSubsetOptions?: LoadSubsetOptions } | undefined)?.loadSubsetOptions ?? {};
        // The reading pane's row, which no list may own, e.g. from a link.
        const conversationId = idOf(options);
        if (conversationId !== undefined) {
          return loadOne(client, conversationId, rows.held?.(conversationId));
        }
        // A search's results, which the search read.
        const ids = idsOf(options);
        if (ids !== undefined) return loadFound(ids, rows.found?.(), rows.held);
        return loadConversations(options, ties, signal);
      },
    }),
  );
  // The synced row, without an action's guess: a guess loaded as the
  // subset's row would become synced, and the action's refusal could not
  // put the row back.
  rows.held = (key) => conversations.base.get(key);
  rows.found = () => foundRows.get(conversations);
  conversations.createIndex((conversation) => conversation.dateMs);
  return conversations;
}

// Conversations is the collection of one member's conversations.
export type Conversations = ReturnType<typeof createConversations>;

// createPlaceCounts returns the place counts collection over client: each
// account's places with their unread counts, read whole. id tells it from
// the collections before it.
//
// DESIGN: it shares List Places' query with the place route's loader, and
// reads it again only when refreshMail does, so a page reads the places
// once.
function createPlaceCounts(client: QueryClient, id: string) {
  return createCollection(
    queryCollectionOptions({
      id,
      queryKey: getListPlacesQueryKey(),
      queryClient: client,
      queryFn: ({ signal }) => listPlaces({ signal }),
      select: (response: ResponsePlacesOutput) => response.data.places,
      getKey: (place: PlaceCount) => placeKey(place.accountId, place.place),
      refetchInterval: false,
      refetchOnWindowFocus: false,
      refetchOnReconnect: false,
      // A read the loader just made serves; refreshMail reads it again.
      refetchOnMount: false,
      staleTime: 0,
    }),
  );
}

// PlaceCounts is the collection of one member's places with their unread
// counts.
export type PlaceCounts = ReturnType<typeof createPlaceCounts>;

// MailData is one member's mail on a client: conversations, which a failed
// list replaces (restartMail); placeCounts, which the sidebar shows;
// stop, which ends their refresh; and checking, which reads them often for
// checkingForMs, as a Check for new mail starts.
export type MailData = {
  conversations: Conversations;
  placeCounts: PlaceCounts;
  stop: () => void;
  checking: () => void;
};

// createMail returns client's mail data, number telling its collections
// from those before them. Every newestEveryMs, when the tab shows
// again, and back online, it reads each open list's newest page and the
// places again (refreshMail), so a count and the rows it counts change
// together.
function createMail(client: QueryClient, number: number): MailData {
  const refresh = () => void refreshMail(client);
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
  let checkingTimer: ReturnType<typeof setInterval> | undefined;
  return {
    conversations: createConversations(client, `conversations-${number}`),
    placeCounts: createPlaceCounts(client, `place-counts-${number}`),
    stop: () => {
      clearInterval(timer);
      clearInterval(checkingTimer);
      document.removeEventListener("visibilitychange", onVisible);
      stopOnline();
    },
    checking: () => {
      clearInterval(checkingTimer);
      const until = Date.now() + checkingForMs;
      checkingTimer = setInterval(() => {
        if (Date.now() >= until) clearInterval(checkingTimer);
        refresh();
      }, checkingEveryMs);
    },
  };
}

// refreshMail reads again what the mail pages on client show: each open
// list's newest page, the places' unread counts, and the accounts, whose
// sync status the sidebar words; it settles when they have.
//
// DESIGN: it reads again and marks nothing out of date: only a write does
// that, and a loader waits for a marked read (cachedRead), so a refresh
// that marked the accounts would hold up the next visit, or fail it.
export async function refreshMail(client: QueryClient): Promise<void> {
  await Promise.all([
    refreshNewest(client),
    client.refetchQueries({ queryKey: getListPlacesQueryKey(), type: "active" }),
    client.refetchQueries({ queryKey: getListConnectedAccountsQueryKey(), type: "active" }),
  ]);
}

// refreshNewest reads again, on client, the newest page of each list open
// and the conversation open in the reading pane, with its messages, and
// settles when they have; older pages stay as they are.
//
// DESIGN: the open conversation with the lists, so a row the pane holds
// after its list let it go is as current as the list.
export async function refreshNewest(client: QueryClient): Promise<void> {
  await client.refetchQueries({
    queryKey: conversationsKey,
    type: "active",
    predicate: (query) => {
      const options = (query.meta as { loadSubsetOptions?: LoadSubsetOptions } | undefined)
        ?.loadSubsetOptions;
      if (!options) return false;
      // A list's newest page: a load with a limit, from the newest.
      const newest = !options.cursor && !options.offset && options.limit !== undefined;
      return newest || idOf(options) !== undefined;
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
    data = createMail(client, created);
    mailByClient.set(client, data);
  }
  return data;
}

let restarted = 0;

// restartMail replaces client's conversations collection with an empty
// one and drops its loads, e.g. after a list's first page failed, which
// leaves the collection failed too. The lists on the old one end; the
// places stay.
export function restartMail(client: QueryClient): void {
  const data = mailByClient.get(client);
  client.removeQueries({ queryKey: conversationsKey });
  if (!data) return;
  const failed = data.conversations;
  restarted++;
  data.conversations = createConversations(client, `conversations-restart-${restarted}`);
  void failed.cleanup({ dependents: true });
}

// dropMail ends client's mail collections, if any: their refresh, and the
// lists still open on them, e.g. as the session ends on a mail page.
function dropMail(client: QueryClient): void {
  const data = mailByClient.get(client);
  if (!data) return;
  mailByClient.delete(client);
  data.stop();
  void data.conversations.cleanup({ dependents: true });
  void data.placeCounts.cleanup({ dependents: true });
}

// One member's mail ends with their session (session-cache.ts): the next
// mailData starts empty.
// The module lives as long as the page, so it never stops listening.
void onAccountCacheClear(dropMail);
