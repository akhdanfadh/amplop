import { keepPreviousData, useInfiniteQuery } from "@tanstack/react-query";
import { getLocalTimeZone, parseDate, today, type CalendarDate } from "@internationalized/date";
import * as z from "zod";
import { searchConversations } from "@/api/generated/inbok";
import type { FoundConversationOutput } from "@/api/generated/model/foundConversation.zod";
import {
  searchConversationsParamsFromMax,
  searchConversationsParamsLimitMax,
  searchConversationsParamsPlaceMax,
  searchConversationsParamsQMax,
  type SearchConversationsParams,
} from "@/api/generated/model/searchConversationsParams.zod";
import { conversationSearchPageTotalMax } from "@/api/generated/model/conversationSearchPage.zod";
import { retryAfterDelay } from "@/api/retry-after";
import { ApiError } from "@/api/transport";
import { isPlace, placeSearch } from "./address";
import type { Place } from "./places";

// The address of a search, /mail/search, a mail address's place
// "search": the words, q; the places, any of which a conversation is in,
// none for All mail; the senders, any of whom sent the matched message;
// the date, a choice relative to today, or a day it is since and one it
// is before; and whether it has an attachment. The account is the mail
// address's own (placeSearch). Each value an old or edited link spoils is
// dropped, so the link still searches.
export const searchFilters = z.object({
  q: z.string().optional().catch(undefined),
  place: z
    .array(z.string())
    .optional()
    .catch(undefined)
    // The API takes at most this many; each is a view or a label's ID.
    .transform((places) => places?.filter(isPlace).slice(0, searchConversationsParamsPlaceMax)),
  from: z
    .array(z.string())
    .optional()
    .catch(undefined)
    .transform((from) => from?.slice(0, searchConversationsParamsFromMax)),
  date: z.enum(["7d", "30d", "1y", "older"]).optional().catch(undefined),
  since: z.iso.date().optional().catch(undefined),
  before: z.iso.date().optional().catch(undefined),
  has: z.enum(["attachment"]).optional().catch(undefined),
});

// mailSearch reads a place's address's search: its account, and on
// /mail/search the search, which other places ignore.
export const mailSearch = placeSearch.extend(searchFilters.shape);

// SearchFilters is a search as its address holds it.
// Each part may be left out.
export type SearchFilters = Partial<z.output<typeof searchFilters>>;

// FilterChange is a change to a search: its filters, or the account it
// searches, undefined for every account.
export type FilterChange = SearchFilters & { account?: number | undefined };

// DatePreset is a date choice relative to today.
export type DatePreset = NonNullable<SearchFilters["date"]>;

// The fewest characters a search's words start at: one letter matches
// too much to be worth a request (DESIGN.md "Search").
export const minWords = 2;

// words returns what of q a search sends: q without the spaces around
// it, if it is long enough to search.
export function words(q: string | undefined): string | undefined {
  const trimmed = q?.trim() ?? "";
  return trimmed.length >= minWords ? trimmed : undefined;
}

// filtered reports whether filters narrow the search beyond its words.
export function filtered(filters: SearchFilters): boolean {
  return (
    (filters.place?.length ?? 0) > 0 ||
    (filters.from?.length ?? 0) > 0 ||
    filters.date !== undefined ||
    filters.since !== undefined ||
    filters.before !== undefined ||
    filters.has !== undefined
  );
}

// The most characters the API takes as a search's words.
export const mostWords = searchConversationsParamsQMax;

// The most places the API searches at once.
export const mostPlaces = searchConversationsParamsPlaceMax;

// tooLong reports whether q's words are longer than the API takes, counted
// in characters, as the API counts them, not in UTF-16 units.
export function tooLong(q: string | undefined): boolean {
  // oxlint-disable-next-line typescript/no-misused-spread -- code points are what the API's maxLength counts (JSON Schema).
  return [...(q?.trim() ?? "")].length > mostWords;
}

// searches reports whether filters ask for anything the API takes: words
// to find, or a filter, which the API searches without words, newest
// first. An account alone asks for nothing: it is the mail address's
// scope, not a search.
export function searches(filters: SearchFilters): boolean {
  if (tooLong(filters.q)) return false;
  return words(filters.q) !== undefined || filtered(filters);
}

// withSpamAndTrash returns places, or All mail for none, with Spam and
// Trash added, or undefined when that would be more places than the API
// searches at once.
export function withSpamAndTrash(places: readonly Place[] | undefined): Place[] | undefined {
  const next = [...new Set([...(places && places.length > 0 ? places : ["all"]), "spam", "trash"])];
  return next.length <= mostPlaces ? next : undefined;
}

// All mail, the places a search with none searches.
const allMail: readonly Place[] = ["all"];

// searchPlaces returns the places filters search, All mail for none: the
// filters' own array, or one kept for good, so a list keyed on it sees no
// change while they don't.
export function searchPlaces(filters: SearchFilters): readonly Place[] {
  return filters.place && filters.place.length > 0 ? filters.place : allMail;
}

// dayStart returns the instant day starts at in the browser's time zone,
// as the API takes it.
function dayStart(day: CalendarDate): string {
  return day.toDate(getLocalTimeZone()).toISOString();
}

// searchParams returns the API's parameters for filters, of account alone
// when given; a date choice counts from now's day, from local midnight.
export function searchParams(
  filters: SearchFilters,
  account: number | undefined,
  now: CalendarDate = today(getLocalTimeZone()),
): SearchConversationsParams {
  let since: string | undefined;
  let before: string | undefined;
  switch (filters.date) {
    case "7d":
      since = dayStart(now.subtract({ days: 7 }));
      break;
    case "30d":
      since = dayStart(now.subtract({ days: 30 }));
      break;
    case "1y":
      since = dayStart(now.subtract({ years: 1 }));
      break;
    case "older":
      before = dayStart(now.subtract({ years: 1 }));
      break;
    default:
      if (filters.since) since = dayStart(parseDate(filters.since));
      if (filters.before) before = dayStart(parseDate(filters.before));
  }
  return {
    q: words(filters.q),
    account,
    place: filters.place && filters.place.length > 0 ? filters.place : undefined,
    from: filters.from && filters.from.length > 0 ? filters.from : undefined,
    since,
    before,
    has: filters.has,
  };
}

// Found is one search result: the conversation, as lists show it, and the
// message that matched best.
export type Found = FoundConversationOutput;

// How many results a page holds: the most the API sends.
const pageSize = searchConversationsParamsLimitMax;

// The most results the API counts or pages through.
export const mostResults = conversationSearchPageTotalMax;

const tooManyRequests = 429;

// How many times a search the server throttled asks again, each after
// the wait it names (DESIGN.md "Search").
const throttledTries = 5;

// The key under which searches keep their results.
export const searchResultsKey = ["mail", "search"] as const;

// useSearchResults returns the pages of a search for params, read 50 at a
// time as the member scrolls, while enabled. Typing keeps the last
// results on screen until the next arrive, and a new search ends the one
// before it, whose answer no one waits for (TanStack Query cancels a read
// left without observers). A throttled search asks again after the wait
// the server names, quietly; a network failure is tried twice more.
//
// DESIGN: kept only while shown (gcTime 0): its rows join the mail
// collection while shown, where actions change them, and a search
// returned to later reads again rather than show rows from before them.
export function useSearchResults(params: SearchConversationsParams, enabled: boolean) {
  return useInfiniteQuery({
    queryKey: [...searchResultsKey, params],
    queryFn: ({ pageParam, signal }) =>
      searchConversations({ ...params, page: pageParam, limit: pageSize }, { signal }),
    initialPageParam: 1,
    getNextPageParam: (last, pages) => {
      const loaded = pages.reduce((n, page) => n + page.data.results.length, 0);
      return loaded < last.data.total && last.data.results.length > 0
        ? pages.length + 1
        : undefined;
    },
    enabled,
    placeholderData: keepPreviousData,
    gcTime: 0,
    refetchOnWindowFocus: false,
    retry: (failures, err) => {
      if (err instanceof ApiError && err.status === tooManyRequests) {
        return failures < throttledTries;
      }
      return err instanceof ApiError && err.kind === "network" && failures < 2;
    },
    retryDelay: (failures, err) =>
      (err instanceof ApiError ? retryAfterDelay(err.retryAfter) : undefined) ??
      1_000 * 2 ** failures,
  });
}
