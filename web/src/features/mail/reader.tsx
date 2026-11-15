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
import { Spinner } from "@/components/ui/spinner";
import { clearAccountCache } from "@/features/auth/session-cache";
import { ConversationView } from "@/features/messages/conversation";
import { useLayout } from "@/hooks/use-layout";
import { isCurrent, runAction, type ActionMail } from "./actions";
import { currentMail } from "./conversations";
import type { AppHref } from "./places";
import {
  accountsUnreadable,
  backToList,
  messagesFailed,
  messagesLoading,
  subjectText,
} from "./text";

const unauthorized = 401;
const notFoundStatus = 404;

// Reader is the reading pane's conversation, shown by the conversation
// view (features/messages). It draws the subject and account from the
// conversation's row at once, keeps them when a read fails, and names the
// tab after the subject. Opening an unread conversation marks it read.
//
// back is its list, which a phone, showing one pane at a time, returns to
// with Back in the view's toolbar. A conversation that does not exist,
// e.g. deleted since the list drew it, is Page not found unless it is
// already shown, so a reply being typed stays; an ended session opens
// sign-in.
export function Reader({
  conversationId,
  back,
}: {
  conversationId: string;
  back: AppHref;
}): ReactElement {
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
  // does, so it adds no history; a conversation opened from a link goes to
  // its list in its place.
  const goBack = () => {
    if (router.history.canGoBack()) router.history.back();
    else void navigate({ ...back, replace: true });
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
        onUnauthorized={signOut}
        back={
          phone && (
            <Button variant="ghost" onPress={goBack} className="-ml-2 gap-1 px-2">
              <ArrowLeftIcon aria-hidden="true" className="size-4" />
              {backToList}
            </Button>
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
