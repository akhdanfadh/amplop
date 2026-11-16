import { useEffect, useMemo, useRef, useState, type ReactElement, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Collection } from "react-aria-components";
import { useListConnectedAccounts } from "@/api/generated/inbok";
import type { ListedAccount } from "@/api/generated/model";
import { userMessage } from "@/api/problem";
import { useTopBarSlot } from "@/components/frame";
import { ReadError } from "@/components/read-error";
import { Empty, EmptyDescription, EmptyTitle } from "@/components/ui/empty";
import { GridList, GridListEnd } from "@/components/ui/grid-list";
import { clearAccountCache } from "@/features/auth/session-cache";
import { useToday } from "@/features/mail/dates";
import { Placeholder } from "@/features/mail/list";
import { useRowSize } from "@/features/mail/list-state";
import { MailPanes } from "@/features/mail/panes";
import { createOpenStore } from "@/features/mail/row";
import { useFocusOnOpen } from "@/hooks/use-focus-on-open";
import { useLayout } from "@/hooks/use-layout";
import { useOutbox } from "./outbox";
import { OutboxRow } from "./row";
import {
  outboxEmptyText,
  outboxEmptyTitle,
  outboxEnd,
  outboxFailed,
  outboxListName,
  outboxLoading,
  outboxName,
  outboxStale,
} from "./text";

const unauthorized = 401;

// OutboxPage is the Outbox (DESIGN.md "Outbox"): its list of the member's
// messages beside reader, the reading pane, as a place's mail. openId is
// the message reader holds; pane says what was opened: the list, with
// nothing in the reading pane, or the reader.
//
// DESIGN: no search box and no picking: search finds mail, which these
// messages are not until Gmail has them, and the Outbox has no action on
// several messages at once.
export function OutboxPage({
  openId,
  pane,
  reader,
}: {
  openId: string | undefined;
  pane: "list" | "reader";
  reader: ReactNode;
}): ReactElement {
  const headingRef = useRef<HTMLHeadingElement>(null);
  const topBar = useTopBarSlot();
  const slot = topBar?.box ?? null;
  const inBar = topBar?.shown ?? false;
  // In a top bar the heading waits for its box, as a place's does
  // (place-page.tsx), so it is drawn once, in its place, and focused there.
  const placed = !inBar || slot !== null;
  // The page focuses its heading when it opens (DESIGN.md "Focus"); what
  // the reading pane shows takes focus itself.
  useFocusOnOpen(headingRef, pane === "list" && placed ? "outbox" : null);
  // On a phone the open message has its own bar, so the heading leaves the
  // top bar to the wordmark.
  const hidden = useLayout() === "phone" && pane === "reader";
  const heading = (
    <h1
      ref={headingRef}
      id="outbox-heading"
      tabIndex={-1}
      className="truncate text-lg font-semibold"
    >
      {outboxName}
    </h1>
  );

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <MailPanes
        pane={pane}
        list={
          <main className="flex h-full flex-col" aria-labelledby="outbox-heading">
            {slot ? !hidden && createPortal(heading, slot) : null}
            {!inBar && (
              // A place's header's size (place-page.tsx), without its buttons.
              <header className="flex min-h-12 items-center border-b py-2 pr-2 pl-4">
                {heading}
              </header>
            )}
            <OutboxList openId={openId} />
          </main>
        }
        reader={reader}
      />
    </div>
  );
}

// OutboxList lists the member's newest 50 messages from inbok, newest
// first, each opening in the reading pane (DESIGN.md "Outbox"). openId is
// the message the pane shows.
//
// DESIGN: the list has no aria-busy: it is read again every 2 s while a
// message is being sent, and marking each read busy would make screen
// readers hold back or restart it.
function OutboxList({ openId }: { openId: string | undefined }): ReactElement {
  const client = useQueryClient();
  const navigate = useNavigate();
  const list = useOutbox();
  const accounts = useListConnectedAccounts();
  const now = useToday();
  const { size: rowSize, probe } = useRowSize();
  const [openStore] = useState(() => createOpenStore(openId));
  useEffect(() => openStore.set(openId), [openStore, openId]);
  const accountList = accounts.data?.data.accounts;
  const accountsById = useMemo(
    () => new Map<number, ListedAccount>(accountList?.map((a) => [a.id, a])),
    [accountList],
  );
  const ringed = (accountList?.length ?? 0) > 1;
  const expired = [list.error, accounts.error].some((err) => err?.status === unauthorized);
  useEffect(() => {
    if (!expired) return;
    clearAccountCache(client);
    void navigate({ to: "/sign-in", replace: true });
  }, [expired, client, navigate]);

  const messages = list.data?.data.messages;
  if (!messages && list.error && !expired)
    return (
      <div className="p-4">
        <ReadError
          message={`${outboxFailed} ${userMessage(list.error)}`}
          error={list.error}
          retrying={list.isFetching}
          onRetry={() => void list.refetch()}
        />
      </div>
    );
  if (!messages || expired) return <Placeholder loading={outboxLoading} />;
  // A failed reread keeps what was read, even an empty list, saying it may
  // be out of date.
  const stale = list.error && (
    <div className="border-b p-4">
      <ReadError
        message={`${outboxStale} ${userMessage(list.error)}`}
        error={list.error}
        retrying={list.isFetching}
        onRetry={() => void list.refetch()}
      />
    </div>
  );
  if (messages.length === 0)
    return (
      <>
        {stale}
        <Empty className="px-4">
          <EmptyTitle>{outboxEmptyTitle}</EmptyTitle>
          <EmptyDescription>{outboxEmptyText}</EmptyDescription>
        </Empty>
      </>
    );

  return (
    <div className="relative flex min-h-0 flex-1 flex-col">
      {/* 1 rem square, so the rows follow the browser's text size. */}
      <span
        ref={probe}
        aria-hidden="true"
        className="pointer-events-none invisible absolute size-4"
      />
      {stale}
      <GridList aria-label={outboxListName} rowSize={rowSize} className="min-h-0 flex-1">
        <Collection items={messages} dependencies={[now, accountsById, ringed]}>
          {(message) => {
            const account = accountsById.get(message.connectedAccountId);
            return (
              <OutboxRow
                message={message}
                account={
                  account && { color: account.color, email: account.email, name: account.name }
                }
                ringed={ringed}
                openStore={openStore}
                now={now}
              />
            );
          }}
        </Collection>
        <GridListEnd>{outboxEnd}</GridListEnd>
      </GridList>
    </div>
  );
}
