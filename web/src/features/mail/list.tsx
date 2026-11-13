import { useMemo, useState, type ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useLiveInfiniteQuery } from "@tanstack/react-db";
import { Collection } from "react-aria-components";
import { useListConnectedAccounts } from "@/api/generated/inbok";
import type { ListedAccountOutput } from "@/api/generated/model/listedAccount.zod";
import { GridList, GridListEnd, GridListLoadMore } from "@/components/ui/grid-list";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { mailData, placeWhere } from "./conversations";
import { useRowSize, useToday } from "./list-state";
import type { Place } from "./places";
import { ConversationRow, type RowAccount } from "./row";
import { listEnd, listLoading, listLoadingMore, listName } from "./text";

// Rows per page: the API's default page.
const pageSize = 50;

// Placeholder rows while the first page loads: about a window's worth.
const placeholderRows = 10;

// ConversationList is the list of a place's conversations across the
// member's accounts (DESIGN.md "Message list"): newest first, 50 at a time
// as the member scrolls.
export function ConversationList({ place }: { place: Place }): ReactElement {
  const client = useQueryClient();
  // Taken once: after sign-out drops the member's mail, this list, still
  // drawn for a moment, must not start a new collection and read again.
  const [{ conversations }] = useState(() => mailData(client));
  const accounts = useListConnectedAccounts();
  const accountList = accounts.data?.data.accounts;
  const accountsById = useMemo(
    () => new Map<number, ListedAccountOutput>(accountList?.map((a) => [a.id, a])),
    [accountList],
  );

  // One live query per list, with its own key (ADR031, TanStack DB gate,
  // rule 4); the collection's ID tells one member's lists from the ones
  // before.
  const list = useLiveInfiniteQuery(
    (q) =>
      q
        .from({ c: conversations })
        .where(({ c }) => placeWhere(c, place, undefined))
        .orderBy(({ c }) => c.dateMs, "desc")
        .orderBy(({ c }) => c.id, { direction: "desc", stringSort: "lexical" }),
    { pageSize, queryKey: ["mail-list", conversations.id, place] },
  );

  const { size: rowSize, probe } = useRowSize();
  const now = useToday();

  if (list.isLoading || list.isIdle || list.isCleanedUp) return <Placeholder />;

  const ringed = (accountList?.length ?? 0) > 1;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* 1 rem square, so the rows follow the browser's text size. */}
      <span
        ref={probe}
        aria-hidden="true"
        className="pointer-events-none invisible absolute size-4"
      />
      <GridList aria-label={listName} rowSize={rowSize} className="min-h-0 flex-1">
        <Collection items={list.data} dependencies={[now, accountsById, ringed]}>
          {(conversation) => (
            <ConversationRow
              conversation={conversation}
              account={rowAccount(accountsById.get(conversation.accountId))}
              ringed={ringed}
              now={now}
            />
          )}
        </Collection>
        {list.hasNextPage ? (
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
          <GridListEnd>{listEnd}</GridListEnd>
        )}
      </GridList>
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

// rowAccount returns what a row shows of account, if the list knows it.
function rowAccount(account: ListedAccountOutput | undefined): RowAccount | undefined {
  return account && { color: account.color, email: account.email, name: account.name };
}
