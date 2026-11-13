import { useEffect, useRef, useState, type ReactElement } from "react";
import { Link } from "@tanstack/react-router";
import * as z from "zod";
import { useGetOutgoingMessage, useListConnectedAccounts } from "@/api/generated/inbok";
import type { OutgoingMessage } from "@/api/generated/model";
import { userMessage } from "@/api/problem";
import { lineLink } from "@/components/link-style";
import { ReadError } from "@/components/read-error";
import { Button } from "@/components/ui/button";
import { Sheet, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { useLayout } from "@/hooks/use-layout";
import { pollWhile, sendPollMs } from "./poll";
import {
  othersReceived,
  replyDefaults,
  type AnsweredMessage,
  type ReplyMode,
  type ReplyValues,
} from "./reply-defaults";
import { ReplyEditor } from "./reply-editor";
import { MessageStatus } from "./status";
import {
  accountsUnreadable,
  inProgress,
  notAnswerable,
  readErrorMessage,
  replyLabels,
  replyStatusStale,
} from "./text";

const unauthorized = 401;
const notFound = 404;

// A message's ID, which the client puts in the API's path as is.
const messageId = z.uuid();

// An open editor: its mode, what it starts with, the message it answers,
// whether it is a phone's full-screen sheet, and a number that gives each
// editor its own form and Idempotency-Key.
//
// DESIGN: the message and the layout are fixed when it opens. A newer
// last message, synced while the member types, must not turn the reply
// into an answer to another message; and moving the editor between the
// page and a sheet when the window crosses the phone width would remount
// it, losing what was typed and its Idempotency-Key.
type Editor = {
  mode: ReplyMode;
  initial: ReplyValues;
  answeredId: string;
  sheet: boolean;
  serial: number;
};

// A saved reply whose status shows, and the message it answers, which its
// Edit and send answers again.
type Sent = { id: string; answeredId: string };

// ConversationReply is the bottom of a conversation: Reply and Reply all
// (when someone else received message), which open one inline editor
// answering message, the conversation's last; then, once sent, the reply's
// status until the member replies again. On a phone the editor fills the
// screen. onUnauthorized runs on a 401.
//
// DESIGN: it takes the answered message as props and reads only the
// member's accounts, so the conversation view mounts it as it is
// (DESIGN.md, "Compose and reply").
export function ConversationReply({
  message,
  onUnauthorized,
}: {
  message: AnsweredMessage;
  onUnauthorized: () => void;
}): ReactElement {
  const layout = useLayout();
  const read = useListConnectedAccounts();
  const accounts = read.data?.data.accounts;
  const replyRef = useRef<HTMLButtonElement>(null);
  const serial = useRef(0);
  // The open editor's Discard, for the phone sheet's Escape.
  const discardRef = useRef<(() => void) | null>(null);
  const [editor, setEditor] = useState<Editor | null>(null);
  const [sent, setSent] = useState<Sent | null>(null);
  // Set when the editor closes without sending, so focus returns to Reply.
  const returnFocus = useRef(false);
  const expired = read.error?.status === unauthorized;

  useEffect(() => {
    if (expired) onUnauthorized();
  }, [expired, onUnauthorized]);
  useEffect(() => {
    if (!returnFocus.current || editor) return;
    returnFocus.current = false;
    replyRef.current?.focus();
  }, [editor]);

  // open shows the editor in mode answering answeredId, starting with
  // initial, or with the defaults for the conversation's last message.
  function open(mode: ReplyMode, initial?: ReplyValues, answeredId = message.id): void {
    if (!accounts) return;
    serial.current++;
    setEditor({
      mode,
      initial: initial ?? replyDefaults(message, mode, accounts),
      answeredId,
      sheet: layout === "phone",
      serial: serial.current,
    });
  }

  // edit opens an editor with the content of reply, a refused one that
  // answered answeredId: a new reply to that same message, from reply's
  // account while it can send.
  function edit(reply: OutgoingMessage, answeredId: string): void {
    const usable = accounts?.some(
      (account) => account.id === reply.connectedAccountId && account.status === "usable",
    );
    open(
      "reply",
      {
        connectedAccountId: usable ? reply.connectedAccountId : 0,
        to: reply.to,
        cc: reply.cc,
        bcc: reply.bcc,
        body: reply.body,
      },
      answeredId,
    );
  }

  // Anything else, e.g. ../ segments, would reach another path of the API.
  if (!messageId.safeParse(message.id).success)
    return <p className="text-sm text-muted-foreground">{notAnswerable}</p>;

  if (!accounts) {
    if (read.error && !expired)
      return (
        <ReadError
          message={`${accountsUnreadable} ${userMessage(read.error)}`}
          error={read.error}
          retrying={read.isFetching}
          onRetry={() => void read.refetch()}
        />
      );
    return <output className="text-sm text-muted-foreground">Loading...</output>;
  }

  const form = editor && (
    <ReplyEditor
      key={editor.serial}
      answeredId={editor.answeredId}
      mode={editor.mode}
      accounts={accounts}
      receiving={accounts.find((account) => account.id === message.accountId)}
      initial={editor.initial}
      onSent={(reply) => {
        setSent({ id: reply.id, answeredId: editor.answeredId });
        setEditor(null);
      }}
      onDiscard={() => {
        returnFocus.current = true;
        setEditor(null);
      }}
      onUnauthorized={onUnauthorized}
      discardRef={discardRef}
    />
  );

  return (
    <div className="flex flex-col gap-4">
      {sent && !editor && (
        <ReplyStatus
          key={sent.id}
          id={sent.id}
          onEdit={(reply) => edit(reply, sent.answeredId)}
          onSentAgain={(copy) => setSent({ id: copy.id, answeredId: sent.answeredId })}
          onUnauthorized={onUnauthorized}
        />
      )}
      {form && !editor.sheet && form}
      {form && editor.sheet && (
        // Escape runs the editor's Discard, which asks before typed text is
        // lost; the scrim, a stray tap away, does not close it.
        <Sheet
          isOpen
          onOpenChange={(isOpen) => {
            if (!isOpen) discardRef.current?.();
          }}
          side="bottom"
          className="overflow-y-auto data-[side=bottom]:top-0 data-[side=bottom]:h-dvh data-[side=bottom]:border-t-0"
          isDismissable={false}
          showCloseButton={false}
        >
          <SheetHeader className="pb-0">
            <SheetTitle>{replyLabels[editor.mode]}</SheetTitle>
          </SheetHeader>
          <div className="px-4 pb-4">{form}</div>
        </Sheet>
      )}
      {!editor && (
        <div className="flex flex-wrap gap-3">
          <Button ref={replyRef} variant="outline" onPress={() => open("reply")}>
            {replyLabels.reply}
          </Button>
          {othersReceived(message, accounts) && (
            <Button variant="outline" onPress={() => open("reply-all")}>
              {replyLabels["reply-all"]}
            </Button>
          )}
        </div>
      )}
    </div>
  );
}

// ReplyStatus shows how the send of the reply id goes, reading it again
// while it is being sent, with what the member can do next. onEdit receives
// a refused reply to edit into a new one; onSentAgain receives the copy a
// Send again saved; onUnauthorized runs on a 401.
function ReplyStatus({
  id,
  onEdit,
  onSentAgain,
  onUnauthorized,
}: {
  id: string;
  onEdit: (reply: OutgoingMessage) => void;
  onSentAgain: (copy: OutgoingMessage) => void;
  onUnauthorized: () => void;
}): ReactElement {
  const ref = useRef<HTMLElement>(null);
  const read = useGetOutgoingMessage(id, {
    query: { refetchInterval: pollWhile(sendPollMs, (data) => inProgress(data.data.status)) },
  });
  const accounts = useListConnectedAccounts();
  const reply = read.data?.data;
  const gone = read.error?.status === notFound;
  const current = accounts.data?.data.accounts.find(
    (account) => account.id === reply?.connectedAccountId,
  )?.email;

  useEffect(() => {
    // The editor that held focus is gone; its status takes it.
    ref.current?.focus();
  }, []);
  useEffect(() => {
    if (read.error?.status === unauthorized) onUnauthorized();
  }, [read.error, onUnauthorized]);

  return (
    <section
      ref={ref}
      tabIndex={-1}
      aria-label="Your reply"
      className="flex flex-col gap-3 rounded-lg border p-4 outline-none"
    >
      {reply && !gone && (
        <MessageStatus
          message={reply}
          current={current}
          onSentAgain={onSentAgain}
          onUnauthorized={onUnauthorized}
          onEdit={() => onEdit(reply)}
        />
      )}
      {/* A failed read without data, or a gone reply, replaces the status;
          a failed reread keeps it, saying it may have moved on, as the
          Outbox's page does. Polling stops on a failure; Try again restarts it. */}
      {read.error && read.error.status !== unauthorized && (
        <ReadError
          message={
            reply && !gone
              ? `${replyStatusStale} ${userMessage(read.error)}`
              : readErrorMessage(read.error)
          }
          error={read.error}
          retrying={read.isFetching}
          onRetry={() => void read.refetch()}
        />
      )}
      <p>
        <Link to="/mail/outbox/$id" params={{ id }} className={lineLink}>
          Open in Outbox
        </Link>
      </p>
    </section>
  );
}
