import type { ReactElement } from "react";
import { useNavigate, useRouter } from "@tanstack/react-router";
import { ArrowLeftIcon } from "lucide-react";
import { useListConnectedAccounts } from "@/api/generated/inbok";
import { userMessage } from "@/api/problem";
import { AccountMark, accountName } from "@/components/account-mark";
import { ReadError } from "@/components/read-error";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { Tooltip, TooltipTrigger } from "@/components/ui/tooltip";
import { useToday } from "@/features/mail/dates";
import { backToList, subjectText } from "@/features/mail/text";
import { OpenMessage } from "@/features/messages/open-message";
import { ReadingPane } from "@/features/messages/reading-pane";
import { useLayout } from "@/hooks/use-layout";
import { messageContent, messageHead } from "./message";
import { useOutgoingMessage } from "./outbox";
import { MessageStatus } from "./status";
import {
  messageGone,
  messageLoading,
  messageStatusStale,
  readErrorMessage,
  sentFrom,
} from "./text";

const notFound = 404;

// OutgoingReader is the Outbox's reading pane for the message id
// (DESIGN.md "Outbox"): as a conversation's, a toolbar with Back on a
// phone, the subject and the sending account, then the status in a card
// with what the member can do next, then the message, open. valid false
// means id is no message's ID, which is never read.
export function OutgoingReader({ id, valid }: { id: string; valid: boolean }): ReactElement {
  const navigate = useNavigate();
  const router = useRouter();
  const phone = useLayout() === "phone";
  const today = useToday();
  const read = useOutgoingMessage(id, { enabled: valid });
  // What the message offers depends on its member's accounts.
  const accounts = useListConnectedAccounts();
  const message = read.message;
  const accountList = accounts.data?.data.accounts;
  const account = accountList?.find((a) => a.id === message?.connectedAccountId);

  // Back returns to the list the member came from, as the phone's own back
  // does, when a row of it opened the message; one opened another way,
  // e.g. from the compose window, goes to the Outbox (mail/reader.tsx).
  const goBack = () => {
    if (router.state.location.state.fromList) router.history.back();
    else void navigate({ to: "/mail/outbox", replace: true });
  };

  // An arrow alone, as the conversation's (mail/reader.tsx); on a phone it
  // is the only way back to the list, so every state below keeps it.
  const back = phone && (
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
  );
  // A phone's gutter is 16 px, a wider pane's 24 (DESIGN.md "Panes and
  // widths").
  const gutter = phone ? "p-4" : "p-6";
  // A failed read without data replaces the message, saying why, an ended
  // session too; a failed reread keeps it, unless the message is gone.
  const gone = read.error?.status === notFound;
  if (!valid || !message || gone)
    return (
      <>
        {back && <div className="flex min-h-12 items-center px-4">{back}</div>}
        <div className={gutter}>
          {!valid ? (
            <p role="alert">{messageGone}</p>
          ) : read.error ? (
            <ReadError
              message={readErrorMessage(read.error)}
              error={read.error}
              retrying={read.retrying}
              onRetry={read.retry}
            />
          ) : (
            <output className="flex pending-delay items-center gap-2 py-2 text-sm text-muted-foreground">
              <Spinner />
              {messageLoading}
            </output>
          )}
        </div>
      </>
    );

  return (
    <>
      <title>{`${subjectText(message.subject)} - inbok`}</title>
      <ReadingPane
        // A new message starts afresh: focus, and an earlier Send again's
        // result.
        key={message.id}
        title={subjectText(message.subject)}
        byline={
          account ? (
            <AccountMark color={account.color} name={accountName(account)} />
          ) : (
            // Its account was disconnected since, or is still loading.
            <span translate="no" className="truncate">
              {message.from}
            </span>
          )
        }
        back={back}
      >
        <div className="flex flex-col gap-4">
          {/* A kept message may show a status that has moved on since. */}
          {read.error && (
            <ReadError
              message={`${messageStatusStale} ${userMessage(read.error)}`}
              error={read.error}
              retrying={read.retrying}
              onRetry={read.retry}
            />
          )}
          <MessageStatus
            look="pane"
            message={message}
            accounts={accountList}
            onSentAgain={(copy) =>
              void navigate({
                to: "/mail/outbox/$id",
                params: { id: copy.id },
                // The copy takes the message's place, as choosing another
                // row does, keeping how Back returns to the list.
                replace: true,
                state: (left) => ({ fromList: left.fromList }),
              })
            }
          />
          <OpenMessage
            head={messageHead(message)}
            account={
              account && { name: accountName(account), email: account.email, color: account.color }
            }
            accountLabel={sentFrom}
            today={today}
            content={messageContent(message)}
          />
        </div>
      </ReadingPane>
    </>
  );
}
