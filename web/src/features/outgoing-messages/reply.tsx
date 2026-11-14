import {
  createContext,
  use,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type ReactElement,
  type ReactNode,
} from "react";
import { ReplyAllIcon, ReplyIcon } from "lucide-react";
import { Link } from "@tanstack/react-router";
import * as z from "zod";
import { useGetOutgoingMessage, useListConnectedAccounts } from "@/api/generated/inbok";
import type { ListedAccount, OutgoingMessage } from "@/api/generated/model";
import { userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import { lineLink } from "@/components/link-style";
import { ReadError } from "@/components/read-error";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipTrigger } from "@/components/ui/tooltip";
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
// and a number that gives each editor its own form and Idempotency-Key.
//
// DESIGN: the message is fixed when it opens: a newer last message, synced
// while the member types, must not turn the reply into an answer to
// another message.
type Editor = {
  mode: ReplyMode;
  initial: ReplyValues;
  answered: AnsweredMessage;
  serial: number;
};

// A Reply button: the message it answers, its mode, and whether it is in
// that message's header or after the last message.
type Opener = { messageId: string; mode: ReplyMode; place: "header" | "bottom" };

// A saved reply whose status shows, and the message it answers, which its
// Edit and send answers again.
type Sent = { id: string; answered: AnsweredMessage };

// What a conversation's reply pieces share: the member's accounts as
// read, the one editor and the last sent reply, and what opens or closes
// them.
type Reply = {
  accounts: ListedAccount[] | undefined;
  // The accounts read's failure, other than a 401, and its retry.
  readError: ApiError | null;
  retrying: boolean;
  retry: () => void;
  editor: Editor | null;
  sent: Sent | null;
  open: (answered: AnsweredMessage, mode: ReplyMode, initial?: ReplyValues) => void;
  edit: (reply: OutgoingMessage, answered: AnsweredMessage) => void;
  setSent: (sent: Sent) => void;
  discard: () => void;
  // The bottom's Reply, which takes focus back when the button that opened
  // the editor is gone.
  bottomReplyId: string;
  onUnauthorized: () => void;
};

const ReplyContext = createContext<Reply | null>(null);

// useReply returns what the conversation's ReplyProvider shares.
function useReply(): Reply {
  const reply = use(ReplyContext);
  if (!reply) throw new Error("reply pieces need a ReplyProvider around the conversation");
  return reply;
}

// useAnsweredId returns the ID of the message the open editor answers, or
// null, so the conversation keeps that message unfolded.
export function useAnsweredId(): string | null {
  return use(ReplyContext)?.editor?.answered.id ?? null;
}

// ReplyProvider holds a conversation's one reply editor (DESIGN.md,
// "Reading pane"): which message it answers, in which mode, and the last
// reply sent, for the pieces the conversation places: MessageReplyActions
// in each message's header, ReplyEditorSlot under each message, and
// ConversationReply after the last. onUnauthorized runs on a 401.
//
// DESIGN: one editor per conversation. While it is open every Reply
// button hides, as the bottom's always did, so a second Reply never
// replaces typed text; the editor never moves once open, which would
// remount it and lose its text and Idempotency-Key.
export function ReplyProvider({
  onUnauthorized,
  children,
}: {
  onUnauthorized: () => void;
  children: ReactNode;
}): ReactElement {
  const read = useListConnectedAccounts();
  const accounts = read.data?.data.accounts;
  const serial = useRef(0);
  const bottomReplyId = useId();
  // Which Reply button opened the editor, by its message, mode, and place
  // (a header or the bottom), which takes focus back on Discard.
  const opener = useRef<Opener | null>(null);
  const returnFocus = useRef(false);
  const [editor, setEditor] = useState<Editor | null>(null);
  const [sent, setSent] = useState<Sent | null>(null);
  const expired = read.error?.status === unauthorized;

  useEffect(() => {
    if (expired) onUnauthorized();
  }, [expired, onUnauthorized]);
  useEffect(() => {
    if (!returnFocus.current || editor) return;
    returnFocus.current = false;
    // The Reply buttons hid while the editor was open, so the one that
    // opened it is found again; its message may have folded meanwhile, and
    // a header draws its buttons twice, one hidden by the pane's width. The
    // bottom's Reply stays.
    const pressed = opener.current;
    const again = pressed
      ? [...document.querySelectorAll<HTMLElement>("[data-reply-for]")].find(
          (button) =>
            button.dataset.replyFor === pressed.messageId &&
            button.dataset.replyMode === pressed.mode &&
            button.dataset.replyPlace === pressed.place &&
            button.offsetParent !== null,
        )
      : undefined;
    (again ?? document.getElementById(bottomReplyId))?.focus();
  }, [editor, bottomReplyId]);

  // The fields used, not the read itself, a new object each render.
  const { error: readError, isFetching: retrying, refetch } = read;
  const value = useMemo<Reply>(() => {
    // open shows the editor in mode answering answered, starting with
    // initial, or with the defaults for that message.
    function open(answered: AnsweredMessage, mode: ReplyMode, initial?: ReplyValues): void {
      if (!accounts) return;
      const button = document.activeElement;
      const place = button instanceof HTMLElement ? button.dataset.replyPlace : undefined;
      opener.current =
        place === "header" || place === "bottom" ? { messageId: answered.id, mode, place } : null;
      serial.current++;
      setEditor({
        mode,
        initial: initial ?? replyDefaults(answered, mode, accounts),
        answered,
        serial: serial.current,
      });
    }

    // edit opens an editor with the content of reply, a refused one that
    // answered answered: a new reply to that same message, from reply's
    // account while it can send.
    function edit(reply: OutgoingMessage, answered: AnsweredMessage): void {
      const usable = accounts?.some(
        (account) => account.id === reply.connectedAccountId && account.status === "usable",
      );
      open(answered, "reply", {
        connectedAccountId: usable ? reply.connectedAccountId : 0,
        to: reply.to,
        cc: reply.cc,
        bcc: reply.bcc,
        body: reply.body,
      });
    }

    return {
      accounts,
      readError: expired ? null : readError,
      retrying,
      retry: () => void refetch(),
      editor,
      sent,
      open,
      edit,
      setSent: (next) => {
        setSent(next);
        setEditor(null);
      },
      discard: () => {
        returnFocus.current = true;
        setEditor(null);
      },
      bottomReplyId,
      onUnauthorized,
    };
  }, [
    accounts,
    expired,
    readError,
    retrying,
    refetch,
    editor,
    sent,
    bottomReplyId,
    onUnauthorized,
  ]);
  return <ReplyContext value={value}>{children}</ReplyContext>;
}

// MessageReplyActions is a message's own Reply, and Reply all when someone
// else received it, as icon buttons in its header; both hide while the
// conversation's editor is open.
export function MessageReplyActions({ message }: { message: AnsweredMessage }): ReactNode {
  const { accounts, editor, open } = useReply();
  if (!accounts || editor || !messageId.safeParse(message.id).success) return null;
  return (
    <>
      <IconAction
        label={replyLabels.reply}
        messageId={message.id}
        mode="reply"
        onPress={() => open(message, "reply")}
      >
        <ReplyIcon />
      </IconAction>
      {othersReceived(message, accounts) && (
        <IconAction
          label={replyLabels["reply-all"]}
          messageId={message.id}
          mode="reply-all"
          onPress={() => open(message, "reply-all")}
        >
          <ReplyAllIcon />
        </IconAction>
      )}
    </>
  );
}

// IconAction is a header's icon button, named by its tooltip, which opens
// the editor answering messageId in mode.
function IconAction({
  label,
  messageId: id,
  mode,
  onPress,
  children,
}: {
  label: string;
  messageId: string;
  mode: ReplyMode;
  onPress: () => void;
  children: ReactNode;
}): ReactElement {
  return (
    <TooltipTrigger>
      {/* Its 32 px box overhangs the header's 20 px line, not taller; on
          touch too, where ::before grows its target to 44 px instead of
          the box, so the line keeps its height. Its own colour, the same
          on every line it sits on. */}
      <Button
        variant="ghost"
        size="icon-sm"
        aria-label={label}
        data-reply-for={id}
        data-reply-mode={mode}
        data-reply-place="header"
        className="relative -my-1.5 text-foreground pointer-coarse:size-8 pointer-coarse:before:absolute pointer-coarse:before:-inset-1.5"
        onPress={onPress}
      >
        {children}
      </Button>
      <Tooltip>{label}</Tooltip>
    </TooltipTrigger>
  );
}

// ReplyEditorSlot shows the conversation's editor right below the message
// with messageId, when it answers that message: inline on every screen,
// so an earlier message stays a scroll away while the member writes.
export function ReplyEditorSlot({ messageId: id }: { messageId: string }): ReactNode {
  const reply = useReply();
  const { editor } = reply;
  if (!editor || editor.answered.id !== id) return null;
  return (
    <div className="px-2 pb-3">
      <EditorForm reply={reply} editor={editor} />
    </div>
  );
}

// EditorForm is the open editor's form.
function EditorForm({ reply, editor }: { reply: Reply; editor: Editor }): ReactElement | null {
  if (!reply.accounts) return null;
  return (
    <ReplyEditor
      key={editor.serial}
      answeredId={editor.answered.id}
      mode={editor.mode}
      accounts={reply.accounts}
      receiving={reply.accounts.find((account) => account.id === editor.answered.accountId)}
      initial={editor.initial}
      onSent={(sent) => reply.setSent({ id: sent.id, answered: editor.answered })}
      onDiscard={reply.discard}
      onUnauthorized={reply.onUnauthorized}
    />
  );
}

// ConversationReply is the bottom of a conversation: Reply and Reply all
// (when someone else received message), which open the editor answering
// message, the conversation's last; once sent, the reply's status, where
// the reply will land, until the member replies again.
export function ConversationReply({ message }: { message: AnsweredMessage }): ReactElement {
  const reply = useReply();
  const { accounts, editor, sent } = reply;

  // Anything else, e.g. ../ segments, would reach another path of the API.
  if (!messageId.safeParse(message.id).success)
    return <p className="text-sm text-muted-foreground">{notAnswerable}</p>;

  if (!accounts) {
    if (reply.readError)
      return (
        <ReadError
          message={`${accountsUnreadable} ${userMessage(reply.readError)}`}
          error={reply.readError}
          retrying={reply.retrying}
          onRetry={reply.retry}
        />
      );
    return <output className="text-sm text-muted-foreground">Loading...</output>;
  }

  return (
    <div className="flex flex-col gap-4">
      {sent && !editor && (
        <ReplyStatus
          key={sent.id}
          id={sent.id}
          onEdit={(outgoing) => reply.edit(outgoing, sent.answered)}
          onSentAgain={(copy) => reply.setSent({ id: copy.id, answered: sent.answered })}
          onUnauthorized={reply.onUnauthorized}
        />
      )}
      {!editor && (
        <div className="flex flex-wrap gap-3">
          <Button
            id={reply.bottomReplyId}
            variant="outline"
            data-reply-for={message.id}
            data-reply-mode="reply"
            data-reply-place="bottom"
            onPress={() => reply.open(message, "reply")}
          >
            {replyLabels.reply}
          </Button>
          {othersReceived(message, accounts) && (
            <Button
              variant="outline"
              data-reply-for={message.id}
              data-reply-mode="reply-all"
              data-reply-place="bottom"
              onPress={() => reply.open(message, "reply-all")}
            >
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
