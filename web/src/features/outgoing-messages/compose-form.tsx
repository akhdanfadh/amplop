import { useState, type ReactElement } from "react";
import { revalidateLogic, useSelector } from "@tanstack/react-form";
import { useSendMessage } from "@/api/generated/inbok";
import type { ConnectedAccount, OutgoingMessage } from "@/api/generated/model";
import { useAppForm } from "@/components/form/app-form";
import { useFormRequest } from "@/components/form/request";
import { LeaveWarning } from "@/components/leave-warning";
import { ComposeSchema, composeFields, composeRequest, type ComposeValues } from "./compose-schema";
import { EditorCard, RecipientRows, SendFailure, recipientLists, useEditorSend } from "./editor";
import { leaveText, sendErrorMessage } from "./text";

// ComposeForm writes a new message and sends it from one of accounts, the
// member's connected accounts, oldest first; one that isn't usable needs
// reconnecting and can't be chosen. initial is what the form starts with.
// onSent receives the saved message, whose send goes on in the background;
// onUnauthorized runs on a 401.
export function ComposeForm({
  accounts,
  initial,
  onSent,
  onUnauthorized,
}: {
  accounts: ConnectedAccount[];
  initial: ComposeValues;
  onSent: (message: OutgoingMessage) => void;
  onUnauthorized: () => void;
}): ReactElement {
  const editor = useEditorSend(onUnauthorized);
  const send = useSendMessage(editor.options);
  const request = useFormRequest(send, composeFields);
  const form = useAppForm({
    defaultValues: initial,
    validationLogic: revalidateLogic(),
    validators: { onDynamic: request.check(ComposeSchema) },
    onSubmit: async ({ value, formApi }) => {
      const sent = await request.send(formApi, value, { data: composeRequest(value) });
      if (sent.outcome === "refused") editor.refused(sent.error);
      if (sent.outcome === "done") onSent(sent.data.data);
    },
    // After an edit, the last request's message no longer describes the form.
    listeners: { onChange: () => send.reset() },
  });
  const sending = useSelector(form.store, (state) => state.isSubmitting);
  const formEdited = useSelector(form.store, (state) => !state.isDefaultValue);
  // A recipient list holding text not yet a chip counts as an edit.
  const [typingIn, setTypingIn] = useState<ReadonlySet<string>>(() => new Set());
  const edited = formEdited || typingIn.size > 0;

  return (
    <form.AppForm>
      <form.Form label="New message" className="gap-3">
        <EditorCard send={<form.SubmitButton working="Sending...">Send</form.SubmitButton>}>
          <form.AppField name="connectedAccountId">
            {(field) => <field.FromField label="From" accounts={accounts} />}
          </form.AppField>
          <RecipientRows form={form} fields={recipientLists} onTyping={setTypingIn} />
          <form.AppField name="subject">
            {(field) => <field.SubjectField label="Subject" />}
          </form.AppField>
          <form.AppField name="body">
            {(field) => <field.MessageField label="Message" />}
          </form.AppField>
        </EditorCard>
        <SendFailure
          errors={request.summary(sendErrorMessage)}
          requestId={request.reference}
          error={send.error}
        />
        <LeaveWarning
          active={edited || sending || editor.unconfirmed}
          text={leaveText(editor.leaveState(sending))}
        />
      </form.Form>
    </form.AppForm>
  );
}
