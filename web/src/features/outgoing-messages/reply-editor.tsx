import { useState, type ReactElement } from "react";
import { revalidateLogic, useSelector } from "@tanstack/react-form";
import { Trash2Icon } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { getGetOutgoingMessageQueryKey, useReplyToMessage } from "@/api/generated/inbok";
import type { ConnectedAccount, OutgoingMessage } from "@/api/generated/model";
import { useAppForm } from "@/components/form/app-form";
import { useFormRequest } from "@/components/form/request";
import { LeaveWarning } from "@/components/leave-warning";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipTrigger } from "@/components/ui/tooltip";
import { EditorCard, RecipientRows, SendFailure, recipientLists, useEditorSend } from "./editor";
import type { ReplyMode, ReplyValues } from "./reply-defaults";
import { ReplySchema, replyFields, replyRequest } from "./reply-schema";
import {
  needsReconnectNote,
  otherAccountNote,
  replyErrorMessage,
  replyLabels,
  replyLeaveText,
} from "./text";

// ReplyEditor writes a reply to the message answeredId names and sends it
// from one of accounts, the member's connected accounts; one that isn't
// usable needs reconnecting and can't be chosen. mode names the editor;
// initial is what it starts with; receiving is the account that holds the
// answered message, named when it can't send. onSent receives the saved
// reply, whose send goes on in the background; onDiscard runs when the
// member discards it; onUnauthorized runs on a 401.
export function ReplyEditor({
  answeredId,
  mode,
  accounts,
  receiving,
  initial,
  onSent,
  onDiscard,
  onUnauthorized,
}: {
  answeredId: string;
  mode: ReplyMode;
  accounts: ConnectedAccount[];
  receiving: ConnectedAccount | undefined;
  initial: ReplyValues;
  onSent: (reply: OutgoingMessage) => void;
  onDiscard: () => void;
  onUnauthorized: () => void;
}): ReactElement {
  const client = useQueryClient();
  const [discarding, setDiscarding] = useState(false);
  const editor = useEditorSend(onUnauthorized);
  const send = useReplyToMessage(editor.options);
  const request = useFormRequest(send, replyFields);
  const form = useAppForm({
    defaultValues: initial,
    validationLogic: revalidateLogic(),
    validators: { onDynamic: request.check(ReplySchema) },
    onSubmit: async ({ value, formApi }) => {
      const sent = await request.send(formApi, value, {
        id: answeredId,
        data: replyRequest(value),
      });
      if (sent.outcome === "refused") editor.refused(sent.error);
      if (sent.outcome !== "done") return;
      // The status shows at once, without waiting for a read.
      client.setQueryData(getGetOutgoingMessageQueryKey(sent.data.data.id), sent.data);
      onSent(sent.data.data);
    },
    // After an edit, the last request's message no longer describes the form.
    listeners: { onChange: () => send.reset() },
  });
  const sending = useSelector(form.store, (state) => state.isSubmitting);
  const formEdited = useSelector(form.store, (state) => !state.isDefaultValue);
  // The recipient lists holding text not yet a chip, which counts as an
  // edit: Discard asks first, and leaving warns.
  const [typingIn, setTypingIn] = useState<ReadonlySet<string>>(() => new Set());
  const edited = formEdited || typingIn.size > 0;
  const chosen = useSelector(form.store, (state) => state.values.connectedAccountId);
  const receivingCannotSend =
    receiving !== undefined && receiving.status !== "usable" && initial.connectedAccountId === 0;
  // Gmail threads a reply only within the account that holds the message
  // (ADR006, "Replies"), so another account's reply starts a conversation.
  const otherAccount = receiving !== undefined && chosen !== 0 && chosen !== receiving.id;
  const fromNote = otherAccount
    ? otherAccountNote
    : receivingCannotSend
      ? needsReconnectNote(receiving.email)
      : undefined;

  function discard(): void {
    if (edited) setDiscarding(true);
    else onDiscard();
  }

  return (
    <form.AppForm>
      <form.Form label={replyLabels[mode]} className="gap-3">
        <EditorCard
          send={<form.SubmitButton working="Sending...">Send</form.SubmitButton>}
          after={
            <TooltipTrigger>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label="Discard"
                isDisabled={sending}
                onPress={discard}
              >
                <Trash2Icon aria-hidden="true" />
              </Button>
              <Tooltip>Discard</Tooltip>
            </TooltipTrigger>
          }
        >
          <form.AppField name="connectedAccountId">
            {(field) => <field.FromField label="From" accounts={accounts} description={fromNote} />}
          </form.AppField>
          <RecipientRows form={form} fields={recipientLists} onTyping={setTypingIn} />
          <form.AppField name="body">
            {/* The recipients are filled in; the member writes the message. */}
            {(field) => <field.MessageField label="Message" focus="mount" />}
          </form.AppField>
        </EditorCard>
        <SendFailure
          errors={request.summary(replyErrorMessage)}
          requestId={request.reference}
          error={send.error}
        />
        <AlertDialog isOpen={discarding} onOpenChange={setDiscarding}>
          <AlertDialogHeader>
            <AlertDialogTitle>{replyLeaveText("typed").title}</AlertDialogTitle>
            <AlertDialogDescription>{replyLeaveText("typed").description}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{replyLeaveText("typed").stay}</AlertDialogCancel>
            <AlertDialogAction type="button" variant="destructive" onPress={onDiscard}>
              {replyLeaveText("typed").leave}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialog>
        <LeaveWarning
          active={edited || sending || editor.unconfirmed}
          text={replyLeaveText(editor.leaveState(sending))}
        />
      </form.Form>
    </form.AppForm>
  );
}
