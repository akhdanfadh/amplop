import { useCallback, useEffect, type ReactElement } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeftIcon } from "lucide-react";
import { useNavigate, useRouter } from "@tanstack/react-router";
import { getGetConversationQueryOptions, useListConnectedAccounts } from "@/api/generated/inbok";
import { accountName } from "@/components/account-mark";
import { ReadError } from "@/components/read-error";
import { NotFound } from "@/components/route-states";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { clearAccountCache } from "@/features/auth/session-cache";
import { ConversationView } from "@/features/messages/conversation";
import { useLayout } from "@/hooks/use-layout";
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
// list's row at once, keeps them when a read fails, and names the tab
// after the subject.
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
  const read = useQuery(getGetConversationQueryOptions(conversationId));
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
  // The list's row, read here rather than as the query's placeholder,
  // which the query drops once a read fails: the frame then stays, with
  // the failure and Try again under the title.
  const conversation =
    detail?.conversation ?? currentMail(client)?.conversations.get(conversationId);
  const account = accounts.data?.data.accounts.find((a) => a.id === conversation?.accountId);
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
