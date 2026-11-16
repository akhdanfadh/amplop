import { useCallback, useEffect, useRef, useSyncExternalStore, type ReactElement } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { eq, useLiveQuery } from "@tanstack/react-db";
import { ArrowLeftIcon } from "lucide-react";
import { useNavigate, useRouter } from "@tanstack/react-router";
import { getGetConversationQueryOptions, useListConnectedAccounts } from "@/api/generated/inbok";
import { ApiError } from "@/api/transport";
import { accountName } from "@/components/account-mark";
import { ReadError } from "@/components/read-error";
import { NotFound } from "@/components/route-states";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipTrigger } from "@/components/ui/tooltip";
import { Spinner } from "@/components/ui/spinner";
import { clearAccountCache } from "@/features/auth/session-cache";
import { ConversationView } from "@/features/messages/conversation";
import { useLayout } from "@/hooks/use-layout";
import { isCurrent, runAction, type ActionMail } from "./actions";
import { currentMail } from "./conversations";
import { requestRowFocus } from "./focus-request";
import { perform } from "./perform";
import type { AppHref, Place } from "./places";
import { leavesList, staysInList, type Action } from "./rules";
import { ConversationToolbar } from "./toolbar";
import {
  accountsUnreadable,
  backToList,
  messagesFailed,
  messagesLoading,
  subjectText,
} from "./text";

const unauthorized = 401;
const notFoundStatus = 404;

// ReaderList is the list a conversation was opened from: one place's, or
// a search's, which keeps a conversation in any of its places, of account
// alone when given; key names it for its focus requests (listKey), and
// back is its address.
export type ReaderList = {
  places: readonly Place[];
  account: number | undefined;
  key: string;
  back: AppHref;
};

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
  const { places, account: listAccount, back } = list;
  const client = useQueryClient();
  const navigate = useNavigate();
  const router = useRouter();
  const accounts = useListConnectedAccounts();
  const phone = useLayout() === "phone";
  // Read again with the lists, by inbok's timer, the tab showing again, or
  // the connection coming back (refreshNewest), not on focus too.
  const read = useQuery({
    ...getGetConversationQueryOptions(conversationId),
    refetchOnWindowFocus: false,
  });
  const expired = read.error?.status === unauthorized;
  // signOut opens sign-in once a read finds the session gone.
  const signOut = useCallback(() => {
    clearAccountCache(client);
    void navigate({ to: "/sign-in", replace: true });
  }, [client, navigate]);

  useEffect(() => {
    if (expired) signOut();
  }, [expired, signOut]);

  const detail = read.data?.data;
  const mail = currentMail(client);
  // The conversation's row in the mail collection, which an action
  // anywhere changes at once: the list's, or loaded by ID (ADR031). Read
  // here rather than as the query's placeholder, which the query drops
  // once a read fails: the frame then stays, with the failure and Try
  // again under the title.
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
  const account = accounts.data?.data.accounts.find((a) => a.id === conversation?.accountId);
  const actionMail = mail && {
    client,
    conversations: mail.conversations,
    placeCounts: mail.placeCounts,
  };
  useReadOnOpen(
    actionMail,
    conversation,
    detail?.messages !== undefined && account?.status === "usable",
    signOut,
  );
  // Back returns to the list the member came from, as the phone's own back
  // does, so it adds no history, when a row of it opened the conversation;
  // one opened another way, e.g. from a link or Settings' Back to mail,
  // goes to its list in its place, rather than back out of mail.
  const goBack = () => {
    if (router.state.location.state.fromList) router.history.back();
    else void navigate({ ...back, replace: true });
  };
  // The conversation an action took out of its list, which takes no more
  // actions: going back takes a moment, and a second press of a double
  // click would land on the button's opposite, e.g. Move to Inbox.
  const left = useRef<string | null>(null);
  useEffect(() => {
    left.current = null;
  }, [conversationId]);
  // onAction runs one of the toolbar's actions on the row. One that takes
  // the conversation out of its list, as the guess shows it, goes back to
  // it at once, where the row that took its place takes focus, or the row
  // itself after Mark unread; a refusal then says why on the list. One the
  // provider no longer has goes back too, its row gone, while the address
  // is still the one it was run at.
  const onAction = (action: Action) => {
    if (!row || !actionMail || left.current === row.id) return;
    const leaves = leavesList(row, action, places, listAccount);
    // The address, not the pane's drawn conversation: a row chosen since
    // moves the address at once, while the pane draws this conversation
    // until the next one's page has loaded.
    const at = router.state.location.href;
    void perform({ mail: actionMail, conversation: row, action, onUnauthorized: signOut }).then(
      (outcome) => {
        if (outcome === "gone" && !leaves && router.state.location.href === at) goBack();
      },
    );
    if (!leaves) return;
    left.current = row.id;
    requestRowFocus(list.key, {
      id: row.id,
      dateMs: row.dateMs,
      stays: staysInList(row, action, places, listAccount),
    });
    goBack();
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
      <title>{`${subjectText(conversation.subject)} - inbok`}</title>
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
        onUnauthorized={signOut}
        actions={
          row && (
            <ConversationToolbar
              conversation={row}
              // One place's actions, e.g. Spam's, where the list has one;
              // a search of several follows the conversation, as All mail.
              list={places.length === 1 ? places[0] : "all"}
              providerUrl={detail?.providerUrl}
              onAction={onAction}
            />
          )
        }
        back={
          // An arrow alone, so the toolbar keeps one row at 390 px (ADR031,
          // "Mail actions as built", checkpoint 7c).
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
// account is usable. A refused or failed read puts the row back unsaid
// and is not tried again; one that finds the session ended runs
// onUnauthorized.
function useReadOnOpen(
  mail: ActionMail | undefined,
  conversation: { id: string; unread: boolean } | undefined,
  ready: boolean,
  onUnauthorized: () => void,
): void {
  const visible = useTabVisible();
  // The conversation this opening already decided for.
  const decided = useRef<string | null>(null);
  useEffect(() => {
    if (!mail || !conversation || !ready || !visible) return;
    if (decided.current === conversation.id) return;
    decided.current = conversation.id;
    if (!conversation.unread) return;
    runAction(mail, { action: "read", ids: [conversation.id] }).catch((err: unknown) => {
      if (err instanceof ApiError && err.status === unauthorized && isCurrent(mail))
        onUnauthorized();
    });
  }, [mail, conversation, ready, visible, onUnauthorized]);
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
