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
  getListConnectedAccountsQueryKey,
  getListPlacesQueryKey,
  listConversations,
  listPlaces,
} from "@/api/generated/inbok";
import type { PlaceCountOutput } from "@/api/generated/model/placeCount.zod";
import type { ResponsePlacesOutput } from "@/api/generated/model/responsePlaces.zod";
import type { ConversationRowOutput } from "@/api/generated/model/conversationRow.zod";
import { listConversationsParamsLimitMax } from "@/api/generated/model/listConversationsParams.zod";
import { onAccountCacheClear } from "@/features/auth/session-cache";
import { viewPlaces, type Place } from "./places";
import { viewFlags, type Flag } from "./rules";

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

// How often each open list reads its newest page again, and the places
// their unread counts.
const newestEveryMs = 30_000;
// How often mail is read again while a Check for new mail runs, and for
// how long after it starts: a check that finds a few messages ends within
// seconds, so its mail and counts show (the same as Connected accounts'
// Check now).
const checkingEveryMs = 2_000;
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
// open list's newest page (ADR031, TanStack DB gate, rule 2).
function createConversations(client: QueryClient, id: string) {
  const ties = new Map<string, Ties>();
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

// MailData is one member's mail on a client: conversations, which a failed
// list replaces (restartMail); placeCounts, which the sidebar shows;
// stop, which ends their refresh; and checking, which reads them often for
// checkingForMs, as a Check for new mail starts.
export type MailData = {
  conversations: Conversations;
  placeCounts: ReturnType<typeof createPlaceCounts>;
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
export async function refreshMail(client: QueryClient): Promise<void> {
  await Promise.all([
    refreshNewest(client),
    client.refetchQueries({ queryKey: getListPlacesQueryKey(), type: "active" }),
    client.invalidateQueries({ queryKey: getListConnectedAccountsQueryKey() }),
  ]);
}

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
