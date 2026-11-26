import { useEffect, useMemo, useRef, useSyncExternalStore, type ReactElement } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { eq, useLiveQuery } from "@tanstack/react-db";
import { ArrowLeftIcon } from "lucide-react";
import { getGetConversationQueryOptions, useListConnectedAccounts } from "@/api/generated/amplop";
import { accountName } from "@/components/account-mark";
import { ReadError } from "@/components/read-error";
import { NotFound } from "@/components/route-states";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipTrigger } from "@/components/ui/tooltip";
import { Spinner } from "@/components/ui/spinner";
import { ConversationView } from "@/features/messages/conversation";
import { isSessionEnded } from "@/features/auth/guard";
import { useLayout } from "@/hooks/use-layout";
import { offeredIn, useAct, type ReaderList } from "./act";
import { runAction, type ActionMail } from "./actions";
import { currentMail } from "./conversations";
import { LabelChips } from "./label-chips";
import { labelWords } from "./words";
import { useLabelsIn } from "./labels";
import type { Performed } from "./perform";
import { changersOf } from "./rules";
import { ConversationToolbar } from "./toolbar";
import {
  accountsUnreadable,
  backToList,
  messagesFailed,
  messagesLoading,
  subjectText,
} from "./text";

const notFoundStatus = 404;

// Reader is the reading pane's conversation, shown by the conversation
// view (features/messages). It draws the subject and account from the
// conversation's row at once, keeps them when a read fails, and names the
// tab after the subject. Opening an unread conversation marks it read. Its
// toolbar's actions follow its state in list, the list it was opened from
// (ConversationToolbar); startAt, a message of it, e.g. the one a search
// matched, starts open, scrolled to.
//
// list.back is that list, which a phone, showing one pane at a time,
// returns to with Back in the view's toolbar, as does an action that takes
// the conversation out of the list. A conversation that does not exist,
// e.g. deleted since the list drew it, is Page not found unless it is
// already shown, so a reply being typed stays; an ended session opens
// sign-in.
export function Reader({
  conversationId,
  list,
  startAt,
}: {
  conversationId: string;
  list: ReaderList;
  startAt?: string;
}): ReactElement {
  const { places, account: listAccount } = list;
  const client = useQueryClient();
  const accounts = useListConnectedAccounts();
  const phone = useLayout() === "phone";
  // Read again with the lists, by amplop's timer, the tab showing again, or
  // the connection coming back (refreshNewest), not on focus too.
  const read = useQuery({
    ...getGetConversationQueryOptions(conversationId),
    refetchOnWindowFocus: false,
  });
  const expired = isSessionEnded(read.error);
  const detail = read.data?.data;
  const mail = currentMail(client);
  const labels = useLabelsIn(mail);
  // The conversation's row in the mail collection, which an action anywhere
  // changes at once: the list's, or loaded by ID. Read here rather than as
  // the query's placeholder, which the query drops once a read fails: the
  // frame then stays, with the failure and Try again under the title.
  const { data: row } = useLiveQuery({
    query: (q) =>
      mail
        ? q
            .from({ c: mail.conversations })
            .where(({ c }) => eq(c.id, conversationId))
            .findOne()
        : undefined,
  });
  const conversation = row ?? detail?.conversation;
  const accountList = accounts.data?.data.accounts;
  const account = accountList?.find((a) => a.id === conversation?.accountId);
  const changers = useMemo(() => changersOf(accountList), [accountList]);
  // Whether the conversation's account takes actions, as the API says.
  const changes = conversation !== undefined && changers.has(conversation.accountId);
  const actionMail = mail && {
    client,
    conversations: mail.conversations,
    placeCounts: mail.placeCounts,
  };
  useReadOnOpen(actionMail, conversation, detail?.messages !== undefined && changes);
  // The toolbar's actions run as the open row's do (useAct).
  const { act, goBack } = useAct(actionMail, list, conversationId);
  const onAction = (performed: Performed) => {
    if (row) act(row, performed);
  };
  // What shows in the messages' place, or the whole pane's without a frame.
  const status =
    accounts.isError && !account ? (
      <ReadError
        message={accountsUnreadable}
        error={accounts.error}
        retrying={accounts.isFetching}
        onRetry={() => void accounts.refetch()}
      />
    ) : read.error && !expired ? (
      <ReadError
        message={messagesFailed}
        error={read.error}
        retrying={read.isFetching}
        onRetry={() => void read.refetch()}
      />
    ) : (
      <Loading />
    );

  if (read.error?.status === notFoundStatus && !detail) return <NotFound />;
  // The view needs the conversation and its account: from the list's row
  // at once, or with the messages.
  // A phone's gutter is 16 px, a wider pane's 24 (DESIGN.md "Panes and
  // widths").
  if (!conversation || !account) return <div className={phone ? "p-4" : "p-6"}>{status}</div>;

  return (
    <>
      <title>{`${subjectText(conversation.subject)} - amplop`}</title>
      <ConversationView
        // A new conversation starts afresh: folds, the reply editor, focus.
        key={conversationId}
        subject={conversation.subject}
        account={{
          id: account.id,
          name: accountName(account),
          email: account.email,
          color: account.color,
        }}
        messages={detail?.messages}
        placeholder={status}
        startAt={startAt}
        marks={
          row && (
            <LabelChips
              conversation={row}
              labels={labels}
              listAccount={listAccount}
              removable={changes}
              onArchive={() => onAction("archive")}
              onRemove={(label) =>
                onAction({
                  changes: [{ ids: [row.id], remove: [label.place] }],
                  words: labelWords([], [label.path]),
                })
              }
            />
          )
        }
        actions={
          row && (
            <ConversationToolbar
              conversation={row}
              // One place's actions, e.g. Spam's, where the list has one;
              // a search of several follows the conversation, as All mail.
              list={offeredIn(places)}
              providerUrl={detail?.providerUrl}
              // Labels under their account's name where the list shows
              // several accounts' mail, as picking's menus show them.
              headings={listAccount === undefined && (accountList?.length ?? 0) > 1}
              labels={labels}
              accountName={(id) => {
                const owner = accountList?.find((a) => a.id === id);
                return owner ? accountName(owner) : "";
              }}
              changers={changers}
              onAction={onAction}
            />
          )
        }
        back={
          // An arrow alone, so the toolbar keeps one row at 390 px.
          phone && (
            <TooltipTrigger>
              <Button
                variant="ghost"
                size="icon"
                onPress={goBack}
                aria-label={backToList}
                className="-ml-2"
              >
                <ArrowLeftIcon aria-hidden="true" />
              </Button>
              <Tooltip>{backToList}</Tooltip>
            </TooltipTrigger>
          )
        }
      />
    </>
  );
}

// useReadOnOpen marks conversation read in mail as it opens, once: when
// ready and the tab shows, only while it is unread then. ready says its
// messages are read, which decided the ones that start open, and its
// account takes actions (changersOf). A refused or failed read puts the row back unsaid
// and is not tried again.
function useReadOnOpen(
  mail: ActionMail | undefined,
  conversation: { id: string; unread: boolean } | undefined,
  ready: boolean,
): void {
  const visible = useTabVisible();
  // The conversation this opening already decided for.
  const decided = useRef<string | null>(null);
  useEffect(() => {
    if (!mail || !conversation || !ready || !visible) return;
    if (decided.current === conversation.id) return;
    decided.current = conversation.id;
    if (!conversation.unread) return;
    // A failure has nothing to say: an ended session is the session-end
    // rule's.
    runAction(mail, { action: "read", ids: [conversation.id] }).catch(() => undefined);
  }, [mail, conversation, ready, visible]);
}

// useTabVisible reports whether the page's tab shows.
function useTabVisible(): boolean {
  return useSyncExternalStore(
    (changed) => {
      document.addEventListener("visibilitychange", changed);
      return () => document.removeEventListener("visibilitychange", changed);
    },
    () => document.visibilityState === "visible",
  );
}

// Loading says the messages are on their way, its text in line with the
// title's.
function Loading(): ReactElement {
  return (
    <output className="flex items-center gap-2 py-2 text-sm text-muted-foreground">
      <Spinner />
      {messagesLoading}
    </output>
  );
}
