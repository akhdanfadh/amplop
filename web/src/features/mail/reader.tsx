import { useEffect, useId, useRef, type ReactElement } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { cn } from "@/lib/cn";
import { ArrowLeftIcon } from "lucide-react";
import { useNavigate, useRouter } from "@tanstack/react-router";
import { getGetConversationQueryOptions, useListConnectedAccounts } from "@/api/generated/inbok";
import { AccountMark, accountName } from "@/components/account-mark";
import { ReadError } from "@/components/read-error";
import { NotFound } from "@/components/route-states";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { clearAccountCache } from "@/features/auth/session-cache";
import { useLayout } from "@/hooks/use-layout";
import { currentMail } from "./conversations";
import { fullDate } from "./dates";
import { bracketed, fullNames } from "./people";
import type { AppHref } from "./places";
import { backToList, messagesFailed, messagesLoading, noSubject, recipientsLabel } from "./text";

const unauthorized = 401;
const notFoundStatus = 404;

// Reader is the reading pane's conversation: its subject, account, and
// each message's sender, recipients, date, and preview. It draws from the
// list's row at once while the messages load, and takes focus when it
// opens, so a screen reader starts at its subject.
//
// back is its list, which a phone, showing one pane at a time, returns to.
// A conversation that no longer exists, e.g. deleted since the list drew
// it, is Page not found.
//
// DESIGN: the message bodies are branch 4b's view, which takes this
// pane's place when the two branches meet (ADR031).
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
  const headingId = useId();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const accounts = useListConnectedAccounts();
  const phone = useLayout() === "phone";
  const read = useQuery({
    ...getGetConversationQueryOptions(conversationId),
    placeholderData: () => {
      const row = currentMail(client)?.conversations.get(conversationId);
      return row && { data: { conversation: row, messages: [] } };
    },
  });
  const expired = read.error?.status === unauthorized;

  useEffect(() => {
    headingRef.current?.focus();
  }, [conversationId]);
  useEffect(() => {
    if (!expired) return;
    clearAccountCache(client);
    void navigate({ to: "/sign-in", replace: true });
  }, [expired, client, navigate]);

  const detail = read.data?.data;
  const conversation = detail?.conversation;
  const account = accounts.data?.data.accounts.find((a) => a.id === conversation?.accountId);
  const subject = conversation?.subject.trim() || noSubject;
  // Back returns to the list the member came from, as the phone's own back
  // does, so it adds no history; a conversation opened from a link goes to
  // its list in its place.
  const goBack = () => {
    if (router.history.canGoBack()) router.history.back();
    else void navigate({ ...back, replace: true });
  };

  if (read.error?.status === notFoundStatus) return <NotFound />;

  return (
    <article
      aria-labelledby={headingId}
      className={cn("flex flex-col gap-4 py-4", phone ? "px-4" : "px-6")}
    >
      <title>{`${subject} - inbok`}</title>
      {phone && (
        <Button
          variant="link"
          onPress={goBack}
          className="min-h-11 w-fit gap-1 px-0 text-base font-normal"
        >
          <ArrowLeftIcon aria-hidden="true" className="size-4" />
          {backToList}
        </Button>
      )}
      <header className="flex flex-col gap-1">
        <h2
          ref={headingRef}
          id={headingId}
          tabIndex={-1}
          className="text-subject font-semibold wrap-anywhere"
        >
          {subject}
        </h2>
        {account && (
          <AccountMark
            color={account.color}
            name={accountName(account)}
            className="text-sm text-muted-foreground"
          />
        )}
      </header>
      {read.isPlaceholderData || (read.isPending && !expired) ? (
        <output className="flex items-center gap-2 text-sm text-muted-foreground">
          <Spinner />
          {messagesLoading}
        </output>
      ) : read.error && !expired && !detail ? (
        <ReadError
          message={messagesFailed}
          error={read.error}
          retrying={read.isFetching}
          onRetry={() => void read.refetch()}
        />
      ) : (
        <ol className="flex flex-col gap-3">
          {detail?.messages.map((message) => (
            <li key={message.id} className="flex flex-col gap-1 border-b pb-3 text-sm">
              <div className="flex items-baseline gap-2">
                {/* The address beside the name, so a name alone can't pass
                    for someone else (DESIGN.md "Reading pane"). */}
                <span className="min-w-0 wrap-anywhere">
                  <span className={message.unread ? "font-semibold" : undefined}>
                    {fullNames(message.from, account?.email)}
                  </span>{" "}
                  <span className="text-muted-foreground">{bracketed(message.from)}</span>
                </span>
                <time
                  dateTime={message.date}
                  className="ml-auto shrink-0 text-xs text-muted-foreground"
                >
                  {fullDate(new Date(message.date))}
                </time>
              </div>
              {message.to.length > 0 && (
                <p className="truncate text-muted-foreground">
                  {recipientsLabel} {fullNames(message.to, account?.email)}
                </p>
              )}
              <p className="wrap-anywhere text-muted-foreground">{message.preview}</p>
            </li>
          ))}
        </ol>
      )}
    </article>
  );
}
