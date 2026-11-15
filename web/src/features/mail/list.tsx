import {
  useEffect,
  useMemo,
  useState,
  type FocusEvent,
  type ReactElement,
  type UIEvent,
} from "react";
import { useQueryClient } from "@tanstack/react-query";
import { linkOptions, useNavigate, useRouter } from "@tanstack/react-router";
import { useLiveInfiniteQuery } from "@tanstack/react-db";
import type { HealthNode } from "@tanstack/db";
import { Collection, Link } from "react-aria-components";
import { ArrowUpIcon } from "lucide-react";
import { useCountNewConversations, useListConnectedAccounts } from "@/api/generated/inbok";
import type { ListedAccountOutput } from "@/api/generated/model/listedAccount.zod";
import { userMessage } from "@/api/problem";
import { ApiError } from "@/api/transport";
import { accountName } from "@/components/account-mark";
import { ReadError } from "@/components/read-error";
import { Button, buttonVariants } from "@/components/ui/button";
import { Empty, EmptyContent, EmptyDescription, EmptyTitle } from "@/components/ui/empty";
import { GridList, GridListEnd, GridListLoadMore } from "@/components/ui/grid-list";
import { Skeleton } from "@/components/ui/skeleton";
import { showError } from "@/components/ui/toast";
import { Spinner } from "@/components/ui/spinner";
import { clearAccountCache } from "@/features/auth/session-cache";
import { cn } from "@/lib/cn";
import { runAction } from "./actions";
import { currentMail, mailData, refreshNewest, restartMail } from "./conversations";
import { useToday } from "./dates";
import { useKeepPlace, useRestIntent, useRowSize } from "./list-state";
import type { Place } from "./places";
import { ConversationRow, createOpenStore, type RowAccount } from "./row";
import { placeWhere } from "./rules";
import {
  connectGmail,
  emptyTitle,
  listEnd,
  listFailed,
  listLoading,
  listLoadingMore,
  listName,
  newConversations,
  noAccountsText,
  olderFailedText,
  tryAgain,
  noAccountsTitle,
  refreshFailed,
  refusedText,
  starFailed,
  stillSyncing,
} from "./text";

const unauthorized = 401;

// Rows per page: the API's default page.
const pageSize = 50;

// How often the "N new conversations" count is asked again, as often as
// the list's newest page.
const newCountEveryMs = 30_000;

// Placeholder rows while the first page loads: about a window's worth.
const placeholderRows = 10;

// ConversationList is the list of a place's conversations, of account
// alone when given (DESIGN.md "Message list"): newest first, 50 at a time
// as the member scrolls; new mail waits behind "N new conversations"
// while the member is scrolled down. openId is the conversation the
// reading pane shows; placeName names the place in an empty list's words.
// onRetry starts the list again after a page failed.
export function ConversationList({
  place,
  placeName,
  account,
  openId,
  onRetry,
}: {
  place: Place;
  placeName: string;
  account: number | undefined;
  openId: string | undefined;
  onRetry: () => void;
}): ReactElement {
  const client = useQueryClient();
  const navigate = useNavigate();
  const router = useRouter();
  // Taken once: after sign-out drops the member's mail, this list, still
  // drawn for a moment, must not start a new collection and read again.
  const [{ conversations, placeCounts }] = useState(() => mailData(client));
  const accounts = useListConnectedAccounts();
  const accountList = accounts.data?.data.accounts;
  const accountsById = useMemo(
    () => new Map<number, ListedAccountOutput>(accountList?.map((a) => [a.id, a])),
    [accountList],
  );

  // One live query per list, with its own key (ADR031, TanStack DB gate,
  // rule 4); the collection's ID tells one member's lists, or a list
  // started again (restartMail), from the ones before.
  const list = useLiveInfiniteQuery(
    (q) =>
      q
        .from({ c: conversations })
        .where(({ c }) => placeWhere(c, place, account))
        .orderBy(({ c }) => c.dateMs, "desc")
        .orderBy(({ c }) => c.id, { direction: "desc", stringSort: "lexical" }),
    { pageSize, queryKey: ["mail-list", conversations.id, place, account ?? null] },
  );

  // The newest date shown while the member is scrolled down; newer rows
  // wait behind the "N new conversations" button. null shows them all.
  //
  // DESIGN: applied here, never in the query: as a date bound in the
  // query, TanStack DB reads the whole place each time it moves (ADR031,
  // TanStack DB gate, rule 1).
  const [anchor, setAnchor] = useState<number | null>(null);
  const rows = useMemo(
    () =>
      anchor === null || !list.data.some((row) => row.dateMs > anchor)
        ? list.data
        : list.data.filter((row) => row.dateMs <= anchor),
    [list.data, anchor],
  );
  const hidden = list.data.length - rows.length;
  const newest = list.data[0]?.dateMs;

  const { size: rowSize, probe } = useRowSize();
  const keepPlace = useKeepPlace(rows, rowSize);

  // Scrolled down, the newest row shown holds the list; back at the top
  // with nothing waiting, it is live again.
  const onScroll = (event: UIEvent<HTMLDivElement>) => {
    const box = event.currentTarget;
    keepPlace.record(box);
    if (box.scrollTop > 0 && anchor === null && newest !== undefined) setAnchor(newest);
    else if (box.scrollTop <= 0 && anchor !== null && hidden === 0) setAnchor(null);
  };

  const newCount = useCountNewConversations(
    { place, account, after: new Date(anchor ?? 0).toISOString() },
    { query: { enabled: anchor !== null, refetchInterval: newCountEveryMs } },
  );
  const waiting = anchor === null ? 0 : Math.max(hidden, newCount.data?.data.count ?? 0);
  // How many times the member has shown new conversations: each time,
  // the list is drawn anew, at its top, with its newest row focused, since
  // the button is gone and focus would fall to the page.
  const [shown, setShown] = useState(0);
  // 1. Read the newest page first: the count can be ahead of the list.
  // 2. Show every row, in a list drawn anew.
  const showNew = async () => {
    await refreshNewest(client);
    keepPlace.forget();
    setAnchor(null);
    setShown((n) => n + 1);
  };

  const now = useToday();
  const [openStore] = useState(() => createOpenStore(openId));
  useEffect(() => openStore.set(openId), [openStore, openId]);
  const readAhead = useRestIntent((id) => {
    void router.preloadRoute(conversationHref(id));
  });

  const error = firstError(list.health);
  const expired = error instanceof ApiError && error.status === unauthorized;
  // A load for older rows failed, e.g. the place's next page; the rows
  // already shown stay.
  const olderFailed = list.health?.error != null && list.data.length > 0;
  const refreshing = list.isSyncError && !olderFailed;
  useEffect(() => {
    if (!expired) return;
    clearAccountCache(client);
    void navigate({ to: "/sign-in", replace: true });
  }, [expired, client, navigate]);

  const retry = () => {
    restartMail(client);
    onRetry();
  };
  if (list.isLoadingError && list.data.length === 0 && error instanceof ApiError && !expired) {
    return (
      <div className="p-4">
        <ReadError message={listFailed} error={error} retrying={false} onRetry={retry} />
      </div>
    );
  }
  if (list.isLoading || list.isIdle || list.isCleanedUp || expired) return <Placeholder />;
  if (rows.length === 0 && !list.hasNextPage) {
    return (
      <EmptyList
        place={place}
        placeName={placeName}
        account={account === undefined ? undefined : accountsById.get(account)}
        accounts={accountList}
      />
    );
  }

  const ringed = account === undefined && (accountList?.length ?? 0) > 1;
  function conversationHref(id: string) {
    return linkOptions({
      to: "/mail/$place/$conversationId",
      params: { place, conversationId: id },
      search: { account },
    });
  }

  // star stars or unstars a conversation; every list shows it at once, and
  // a refusal or failure puts it back and says why (DESIGN.md "Feedback").
  // No toast on success: the star stays in view, nor for an answer that
  // arrives once the member's mail has ended, e.g. after sign-out.
  const star = (id: string, starred: boolean) => {
    const current = () => currentMail(client)?.conversations === conversations;
    runAction(
      { client, conversations, placeCounts },
      { action: starred ? "star" : "unstar", ids: [id] },
    ).then(
      (result) => {
        const refused = result.refused[0];
        if (refused && current()) showError(starFailed(starred), refusedText[refused.reason]);
      },
      (err: unknown) => {
        if (!current()) return;
        if (err instanceof ApiError && err.status === unauthorized) {
          clearAccountCache(client);
          void navigate({ to: "/sign-in", replace: true });
          return;
        }
        showError(starFailed(starred), err instanceof ApiError ? userMessage(err) : undefined);
      },
    );
  };

  // Focus resting on a row reads its conversation ahead, as hovering does.
  const onFocus = (event: FocusEvent) => {
    const row =
      event.target instanceof Element ? event.target.closest("[data-conversation]") : null;
    const id = row?.getAttribute("data-conversation");
    if (id) readAhead.rest(id);
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col" onFocus={onFocus} onBlur={readAhead.leave}>
      {/* 1 rem square, so the rows follow the browser's text size. */}
      <span
        ref={probe}
        aria-hidden="true"
        className="pointer-events-none invisible absolute size-4"
      />
      {/* Always present, so a screen reader hears its words change. */}
      <output
        className={cn(
          "text-sm text-muted-foreground",
          refreshing ? "border-b px-4 py-2" : "sr-only",
        )}
      >
        {refreshing ? refreshFailed(clock(conversations.utils.dataUpdatedAt)) : ""}
      </output>
      <output className="sr-only">{waiting > 0 ? newConversations(waiting) : ""}</output>
      <div className="relative flex min-h-0 flex-1 flex-col">
        {waiting > 0 && (
          <div className="pointer-events-none absolute inset-x-0 top-2 z-10 flex justify-center">
            <Button
              size="sm"
              className="pointer-events-auto animate-in shadow-popover duration-200 ease-standard fade-in-0 slide-in-from-top-2"
              onPress={() => void showNew()}
            >
              <ArrowUpIcon aria-hidden="true" />
              {newConversations(waiting)}
            </Button>
          </div>
        )}
        <GridList
          key={shown}
          aria-label={listName}
          rowSize={rowSize}
          // Only once the member pressed "N new", never as the page loads:
          // the button they pressed is gone.
          // oxlint-disable-next-line jsx-a11y/no-autofocus
          autoFocus={shown > 0 ? "first" : undefined}
          onScroll={onScroll}
          className="min-h-0 flex-1"
        >
          <Collection items={rows} dependencies={[now, accountsById, ringed]}>
            {(conversation) => (
              <ConversationRow
                conversation={conversation}
                account={rowAccount(accountsById.get(conversation.accountId))}
                ringed={ringed}
                href={conversationHref(conversation.id)}
                openStore={openStore}
                now={now}
                // React Aria's links never preload: the router reads the
                // conversation ahead, as its own Link would (ADR031).
                onRest={() => readAhead.rest(conversation.id)}
                onLeave={readAhead.leave}
                onStar={(starred) => star(conversation.id, starred)}
              />
            )}
          </Collection>
          {/* After a failed page, nothing loads by itself again: the
              failure below offers Try again. */}
          {list.hasNextPage && !olderFailed ? (
            <GridListLoadMore
              isLoading={list.isFetchingNextPage}
              onLoadMore={() => {
                if (!list.isFetchingNextPage) void list.fetchNextPage();
              }}
              className="justify-center"
            >
              <Spinner />
              <span className="sr-only">{listLoadingMore}</span>
            </GridListLoadMore>
          ) : (
            !olderFailed && <GridListEnd>{listEnd}</GridListEnd>
          )}
        </GridList>
      </div>
      {olderFailed && (
        <div role="alert" className="flex items-center gap-3 border-t px-4 py-2 text-sm">
          <p className="flex-1">{olderFailedText}</p>
          <Button variant="outline" size="sm" onPress={retry}>
            {tryAgain}
          </Button>
        </div>
      )}
    </div>
  );
}

// Placeholder stands for a list whose first page is loading: rows shaped
// like real ones, shown only after 300 ms so a quick load shows nothing
// (DESIGN.md "States").
function Placeholder(): ReactElement {
  return (
    <div className="flex min-h-0 flex-1 pending-delay flex-col overflow-hidden">
      <output className="sr-only">{listLoading}</output>
      {Array.from({ length: placeholderRows }, (_, index) => (
        <div key={index} aria-hidden="true" className="flex h-19 shrink-0 gap-3 border-b px-4 py-2">
          <Skeleton className="size-9 rounded-full" />
          <div className="flex flex-1 flex-col justify-center gap-2">
            <Skeleton className="h-3 w-1/3" />
            <Skeleton className="h-3 w-2/3" />
            <Skeleton className="h-3 w-5/6" />
          </div>
        </div>
      ))}
    </div>
  );
}

// EmptyList says why a list has nothing, with the next step if there is
// one: no account yet, the account (or one of them) still syncing, or
// nothing there.
function EmptyList({
  place,
  placeName,
  account,
  accounts,
}: {
  place: Place;
  placeName: string;
  account: ListedAccountOutput | undefined;
  accounts: ListedAccountOutput[] | undefined;
}): ReactElement {
  if (accounts?.length === 0) {
    return (
      <Empty className="px-4">
        <EmptyTitle>{noAccountsTitle}</EmptyTitle>
        <EmptyDescription>{noAccountsText}</EmptyDescription>
        <EmptyContent>
          <Link
            href={linkOptions({ to: "/settings/connected-accounts" })}
            className={buttonVariants()}
          >
            {connectGmail}
          </Link>
        </EmptyContent>
      </Empty>
    );
  }
  const name = account && accountName(account);
  const stillCopying = account ? syncing(account) : (accounts ?? []).some(syncing);
  return (
    <Empty className="px-4">
      <EmptyTitle>{emptyTitle(place, placeName)}</EmptyTitle>
      {stillCopying && <EmptyDescription>{stillSyncing(name)}</EmptyDescription>}
    </Empty>
  );
}

// syncing reports whether account's first copy of its mail is still
// under way.
function syncing(account: ListedAccountOutput): boolean {
  return account.sync.status === "waiting" || account.sync.status === "running";
}

// rowAccount returns what a row shows of account, if the list knows it.
function rowAccount(account: ListedAccountOutput | undefined): RowAccount | undefined {
  return account && { color: account.color, email: account.email, name: account.name };
}

// firstError returns the cause of the first problem in a live query's
// health tree, e.g. the ApiError of a failed page.
function firstError(node: HealthNode | undefined): unknown {
  if (!node) return undefined;
  const problem = node.error ?? node.failureReason;
  if (problem) return problem.cause;
  for (const source of node.sources) {
    const cause = firstError(source);
    if (cause !== undefined) return cause;
  }
  return undefined;
}

// clock returns the time of day at ms, as the browser writes it.
function clock(ms: number): string {
  return new Date(ms).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
}
