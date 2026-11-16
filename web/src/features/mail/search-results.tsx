import {
  useEffect,
  useEffectEvent,
  useMemo,
  useRef,
  useState,
  type FocusEvent,
  type ReactElement,
  type RefObject,
} from "react";
import { useQueryClient } from "@tanstack/react-query";
import { linkOptions, useNavigate, useRouter } from "@tanstack/react-router";
import { inArray, useLiveQuery } from "@tanstack/react-db";
import { Collection, Button as AriaButton } from "react-aria-components";
import { useListConnectedAccounts } from "@/api/generated/inbok";
import type { ListedAccountOutput } from "@/api/generated/model/listedAccount.zod";
import { ApiError } from "@/api/transport";
import { accountName } from "@/components/account-mark";
import { ReadError } from "@/components/read-error";
import { Button } from "@/components/ui/button";
import { Empty, EmptyContent, EmptyDescription, EmptyTitle } from "@/components/ui/empty";
import { GridList, GridListEnd, GridListLoadMore } from "@/components/ui/grid-list";
import { Spinner } from "@/components/ui/spinner";
import { clearAccountCache } from "@/features/auth/session-cache";
import { holdFound, mailData, toConversation, type Conversation } from "./conversations";
import { useToday } from "./dates";
import { takeRowFocus, useRowFocusRequest } from "./focus-request";
import { Placeholder, rowAccount, syncing } from "./list";
import { useRestIntent, useRowSize } from "./list-state";
import { perform } from "./perform";
import { searchPlace } from "./places";
import { ConversationRow, createOpenStore } from "./row";
import { inList } from "./rules";
import {
  filtered,
  mostResults,
  searchParams,
  searchPlaces,
  searches,
  tooLong,
  useSearchResults,
  withSpamAndTrash,
  mostWords,
  words,
  type FilterChange,
  type Found,
  type SearchFilters,
} from "./search";
import { SearchSentence } from "./search-filters";
import {
  clearFilters,
  noMatches,
  olderResultsFailed,
  resultsCount,
  resultsEnd,
  resultsLoading,
  resultsLoadingMore,
  resultsName,
  searchBusy,
  searchFailed,
  searchOffline,
  searchPrompt,
  searchSpamTrashToo,
  searchThemToo,
  spamTrashLeftOut,
  stillCopying,
  tryAgain,
  wordsTooLong,
} from "./text";

const unauthorized = 401;
const unavailable = 503;

// How many frames a row to focus has to appear in, about half a second.
const maxFocusFrames = 30;

// SearchResults is a search's results in place of a list (DESIGN.md
// "Search"): the filters as a sentence, how many conversations matched,
// then the conversations, most relevant first, each shown through its
// best-matching message with the matched words marked, 50 at a time as
// the member scrolls. filters and account are the search; onChange
// changes them; openId is the conversation the reading pane shows;
// focusKey names the list for the reading pane's focus requests; heading
// takes focus when an action leaves no row to focus.
export function SearchResults({
  filters,
  account,
  onChange,
  openId,
  focusKey,
  heading,
}: {
  filters: SearchFilters;
  account: number | undefined;
  onChange: (change: FilterChange) => void;
  openId: string | undefined;
  focusKey: string;
  heading: RefObject<HTMLElement | null>;
}): ReactElement {
  const client = useQueryClient();
  const navigate = useNavigate();
  const router = useRouter();
  const [{ conversations, placeCounts }] = useState(() => mailData(client));
  const accounts = useListConnectedAccounts().data?.data.accounts;
  const accountsById = useMemo(
    () => new Map<number, ListedAccountOutput>(accounts?.map((a) => [a.id, a])),
    [accounts],
  );

  const asks = searches(filters);
  const params = useMemo(() => searchParams(filters, account), [filters, account]);
  const search = useSearchResults(params, asks);
  // Each conversation once: pages are offsets into a ranking a sync can
  // shift between them, so a later page can repeat an earlier one's last.
  const found = useMemo(() => {
    const seen = new Set<string>();
    return (search.data?.pages.flatMap((page) => page.data.results) ?? []).filter((f) => {
      if (seen.has(f.conversation.id)) return false;
      seen.add(f.conversation.id);
      return true;
    });
  }, [search.data]);
  const total = search.data?.pages[0]?.data.total ?? 0;

  // The results join the mail collection, as a load of their IDs that
  // reads what the search found (holdFound), so an action shows on them at
  // once; their order and matches stay the search's.
  const ids = useMemo(() => {
    holdFound(
      conversations,
      found.map((f) => toConversation(f.conversation)),
    );
    return found.map((f) => f.conversation.id);
  }, [conversations, found]);
  const live = useLiveQuery((q) =>
    ids.length === 0
      ? undefined
      : q.from({ c: conversations }).where(({ c }) => inArray(c.id, ids)),
  );
  const places = searchPlaces(filters);
  // A new page or a new answer is a new load of IDs, which starts out
  // loading: meanwhile each row is the collection's, as an action left it,
  // else as the search found it, so the list stays drawn, scrolled and
  // focused where it was.
  const loading = live.isLoading;
  const rows = useMemo(() => {
    const byId = loading ? undefined : new Map((live.data ?? []).map((row) => [row.id, row]));
    // A result an action took out of the places searched leaves, as it
    // would leave a place's list.
    return found.flatMap((f) => {
      const row = byId
        ? byId.get(f.conversation.id)
        : (conversations.get(f.conversation.id) ?? toConversation(f.conversation));
      return row && inList(row, places, account) ? [{ ...row, found: f }] : [];
    });
  }, [found, live.data, loading, places, account, conversations]);

  const { size: rowSize, probe } = useRowSize();
  const now = useToday();
  const [openStore] = useState(() => createOpenStore(openId));
  useEffect(() => openStore.set(openId), [openStore, openId]);
  const href = (row: Conversation & { found: Found }) =>
    linkOptions({
      to: "/mail/$place/$conversationId",
      params: { place: searchPlace, conversationId: row.id },
      search: { ...filters, account, message: row.found.match.messageId },
    });
  const readAhead = useRestIntent((id) => {
    const row = rows.find((r) => r.id === id);
    if (row) void router.preloadRoute(href(row));
  });
  const listBox = useRef<HTMLDivElement>(null);
  useFocusRequest({ focusKey, openId, order: ids, rows, rowSize, listBox, heading });

  const signOut = () => {
    clearAccountCache(client);
    void navigate({ to: "/sign-in", replace: true });
  };
  const error = search.error instanceof ApiError ? search.error : undefined;
  const expired = error?.status === unauthorized;
  const endSession = useEffectEvent(signOut);
  useEffect(() => {
    if (expired) endSession();
  }, [expired]);

  const star = (conversation: Conversation, starred: boolean) => {
    void perform({
      mail: { client, conversations, placeCounts },
      conversations: [conversation],
      action: starred ? "star" : "unstar",
      onUnauthorized: signOut,
    });
  };
  const onFocus = (event: FocusEvent) => {
    const row =
      event.target instanceof Element ? event.target.closest("[data-conversation]") : null;
    const id = row?.getAttribute("data-conversation");
    if (id) readAhead.rest(id);
  };

  const scope = account === undefined ? accounts : accounts?.filter((a) => a.id === account);
  const copying = (scope ?? []).filter(syncing);
  const leavesOutSpamTrash = !places.includes("spam") && !places.includes("trash");
  // Offered only while the places with Spam and Trash fit the API's limit.
  const spamTrash = leavesOutSpamTrash ? withSpamAndTrash(filters.place) : undefined;
  const searchSpamTrash = () => onChange({ place: spamTrash });

  const header = (
    <div className="flex shrink-0 flex-col gap-0.5 border-b px-4 py-2">
      <SearchSentence filters={filters} account={account} onChange={onChange} />
      {asks && search.data && (
        <p className="text-xs leading-5 text-muted-foreground">
          <output>{resultsCount(total, mostResults)}</output>
          {leavesOutSpamTrash && (
            <>
              {" · "}
              {spamTrashLeftOut}
              {spamTrash && (
                <>
                  {" "}
                  <AriaButton
                    onPress={searchSpamTrash}
                    className="cursor-default rounded-xs font-medium text-foreground underline underline-offset-2 outline-none data-focus-visible:focus-ring pointer-coarse:py-3"
                  >
                    {searchThemToo}
                  </AriaButton>
                </>
              )}
            </>
          )}
        </p>
      )}
    </div>
  );

  let body: ReactElement;
  if (tooLong(filters.q)) {
    body = (
      <Empty className="px-4">
        <EmptyDescription>{wordsTooLong(mostWords)}</EmptyDescription>
      </Empty>
    );
  } else if (!asks) {
    body = (
      <Empty className="px-4">
        <EmptyDescription>{searchPrompt}</EmptyDescription>
      </Empty>
    );
  } else if (search.isError && !search.data && !expired) {
    body = (
      <div className="p-4">
        <ReadError
          message={failureText(error)}
          error={error}
          retrying={search.isFetching}
          onRetry={() => void search.refetch()}
        />
      </div>
    );
  } else if (!search.data || expired) {
    body = <Placeholder loading={resultsLoading} />;
  } else if (total === 0 || (rows.length === 0 && !search.hasNextPage)) {
    body = (
      <Empty className="px-4">
        {/* The words as typed, which can hold a long unbroken word. */}
        <EmptyTitle className="wrap-anywhere">{noMatches(words(filters.q))}</EmptyTitle>
        {copying.length > 0 && (
          <EmptyDescription>{stillCopying(copying.map(accountName))}</EmptyDescription>
        )}
        {(filtered(filters) || spamTrash) && (
          <EmptyContent>
            {filtered(filters) ? (
              <Button
                variant="outline"
                onPress={() =>
                  onChange({
                    place: undefined,
                    from: undefined,
                    date: undefined,
                    since: undefined,
                    before: undefined,
                    has: undefined,
                  })
                }
              >
                {clearFilters}
              </Button>
            ) : (
              <Button variant="outline" onPress={searchSpamTrash}>
                {searchSpamTrashToo}
              </Button>
            )}
          </EmptyContent>
        )}
      </Empty>
    );
  } else {
    const ringed = account === undefined && (accounts?.length ?? 0) > 1;
    const olderFailed = search.isFetchNextPageError;
    body = (
      <div
        ref={listBox}
        className="flex min-h-0 flex-1 flex-col"
        onFocus={onFocus}
        onBlur={readAhead.leave}
      >
        {/* 1 rem square, so the rows follow the browser's text size. */}
        <span
          ref={probe}
          aria-hidden="true"
          className="pointer-events-none invisible absolute size-4"
        />
        <GridList aria-label={resultsName} rowSize={rowSize} className="min-h-0 flex-1">
          <Collection items={rows} dependencies={[now, accountsById, ringed]}>
            {(row) => (
              <ConversationRow
                conversation={row}
                match={row.found.match}
                account={rowAccount(accountsById.get(row.accountId))}
                ringed={ringed}
                markInbox={!(places.length === 1 && places[0] === "inbox")}
                href={href(row)}
                openStore={openStore}
                now={now}
                onRest={() => readAhead.rest(row.id)}
                onLeave={readAhead.leave}
                onStar={(starred) => star(row, starred)}
              />
            )}
          </Collection>
          {search.hasNextPage && !olderFailed ? (
            <GridListLoadMore
              isLoading={search.isFetchingNextPage}
              onLoadMore={() => {
                if (!search.isFetchingNextPage) void search.fetchNextPage();
              }}
              className="justify-center"
            >
              <Spinner />
              <span className="sr-only">{resultsLoadingMore}</span>
            </GridListLoadMore>
          ) : (
            !olderFailed && <GridListEnd>{resultsEnd}</GridListEnd>
          )}
        </GridList>
        {olderFailed && (
          <div role="alert" className="flex items-center gap-3 border-t px-4 py-2 text-sm">
            <p className="flex-1">{olderResultsFailed}</p>
            <Button variant="outline" size="sm" onPress={() => void search.fetchNextPage()}>
              {tryAgain}
            </Button>
          </div>
        )}
      </div>
    );
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {header}
      {body}
    </div>
  );
}

// failureText says why a search failed: the server busy, no connection,
// or another failure.
function failureText(error: ApiError | undefined): string {
  if (error?.status === unavailable) return searchBusy;
  if (error?.kind === "network") return searchOffline;
  return searchFailed;
}

// useFocusRequest carries out the reading pane's request to focus a row
// after an action took its conversation out of the results, or kept it,
// e.g. Mark unread: that row while it stays, else the one after it in
// order, the search's, which still holds a row an action took out, else
// the one before; with none left, heading. Rows on screen only are drawn,
// so the row scrolls into view first.
function useFocusRequest({
  focusKey,
  openId,
  order,
  rows,
  rowSize,
  listBox,
  heading,
}: {
  focusKey: string;
  openId: string | undefined;
  order: readonly string[];
  rows: readonly { id: string }[];
  rowSize: number;
  listBox: RefObject<HTMLDivElement | null>;
  heading: RefObject<HTMLElement | null>;
}): void {
  const request = useRowFocusRequest(focusKey);
  useEffect(() => {
    if (!request || openId !== undefined) return;
    takeRowFocus(focusKey);
    const shown = new Set(rows.map((row) => row.id));
    const at = order.indexOf(request.id);
    const after = at === -1 ? undefined : order.slice(at + 1).find((other) => shown.has(other));
    let before: string | undefined;
    for (let n = at - 1; n >= 0 && before === undefined; n--) {
      if (shown.has(order[n])) before = order[n];
    }
    const id = (request.stays && shown.has(request.id) ? request.id : undefined) ?? after ?? before;
    const index = id === undefined ? -1 : rows.findIndex((row) => row.id === id);
    const grid = listBox.current?.querySelector<HTMLElement>("[role=grid]");
    if (index === -1 || !grid) {
      requestAnimationFrame(() => heading.current?.focus());
      return;
    }
    const top = index * rowSize;
    if (top < grid.scrollTop || top + rowSize > grid.scrollTop + grid.clientHeight) {
      grid.scrollTop = Math.max(0, top - (grid.clientHeight - rowSize) / 2);
    }
    let frames = 0;
    const focus = () => {
      const target = grid.querySelector<HTMLElement>(
        `[data-conversation="${CSS.escape(rows[index].id)}"]`,
      );
      if (target) target.focus();
      else if (grid.isConnected && ++frames < maxFocusFrames) requestAnimationFrame(focus);
    };
    requestAnimationFrame(focus);
  }, [request, openId, focusKey, order, rows, rowSize, listBox, heading]);
}
