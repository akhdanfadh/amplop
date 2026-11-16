import { useEffect, useRef, useState, type ReactElement, type ReactNode } from "react";
import { revalidateLogic, useSelector } from "@tanstack/react-form";
import { Trash2Icon } from "lucide-react";
import { useSendMessage } from "@/api/generated/inbok";
import type { ListedAccount, OutgoingMessage } from "@/api/generated/model";
import { useAppForm } from "@/components/form/app-form";
import { useFormRequest } from "@/components/form/request";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipTrigger } from "@/components/ui/tooltip";
import { ComposeSchema, composeFields, composeRequest, type ComposeValues } from "./compose-schema";
import { EditorCard, RecipientRows, SendFailure, recipientLists, useEditorSend } from "./editor";
import { newMessageTitle, sendErrorMessage, type LeaveState } from "./text";

// The parts of the compose window's top bar that the form owns: title, the
// window's name, which follows the subject; and send, the Send button when
// the look puts it in the top bar, else null.
export type ComposeHeader = { title: string; send: ReactNode };

// ComposeForm writes a new message and sends it from one of accounts, the
// member's connected accounts, oldest first; one that isn't usable needs
// reconnecting and can't be chosen. initial is what the form starts with;
// note, shown above the rows, says where it came from. look is the compose
// window's: "window" puts Send in the bar under the message, "full-screen"
// in header, the window's top bar, which the form draws first. collapsed
// hides all but header while the window is minimised, keeping what is
// typed. onSent receives the saved message, whose send goes on in the
// background; onDiscard runs on Discard; onLoses receives what closing the
// window would lose, whenever that changes; onUnauthorized runs on a 401.
export function ComposeForm({
  accounts,
  initial,
  note,
  look,
  header,
  collapsed,
  onSent,
  onDiscard,
  onLoses,
  onUnauthorized,
}: {
  accounts: ListedAccount[];
  initial: ComposeValues;
  note?: ReactNode;
  look: "window" | "full-screen";
  header: (parts: ComposeHeader) => ReactNode;
  collapsed: boolean;
  onSent: (message: OutgoingMessage) => void;
  onDiscard: () => void;
  onLoses: (loses: LeaveState | null) => void;
  onUnauthorized: () => void;
}): ReactElement {
  const rowsRef = useRef<HTMLDivElement>(null);
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
  const subject = useSelector(form.store, (state) => state.values.subject.trim());
  // A recipient list holding text not yet a chip counts as an edit.
  const [typingIn, setTypingIn] = useState<ReadonlySet<string>>(() => new Set());
  const edited = formEdited || typingIn.size > 0;
  const loses = edited || sending || editor.unconfirmed ? editor.leaveState(sending) : null;

  // What the form started with, for where focus starts.
  const [start] = useState(initial);

  useEffect(() => onLoses(loses), [loses, onLoses]);
  useEffect(() => {
    // Focus starts in the first field still to fill in: From, To, then the
    // start of the message, e.g. for Edit and send.
    const rows = rowsRef.current;
    if (!rows) return;
    if (start.connectedAccountId === 0)
      rows.querySelector<HTMLElement>('[data-slot="select-trigger"]')?.focus();
    else if (start.to.length === 0) rows.querySelector("input")?.focus();
    else {
      const message = rows.querySelector("textarea");
      message?.focus();
      message?.setSelectionRange(0, 0);
    }
  }, [start]);

  const sendButton = <form.SubmitButton working="Sending...">Send</form.SubmitButton>;
  const discard = (
    <TooltipTrigger>
      <Button type="button" variant="ghost" size="icon" aria-label="Discard" onPress={onDiscard}>
        <Trash2Icon aria-hidden="true" />
      </Button>
      <Tooltip>Discard</Tooltip>
    </TooltipTrigger>
  );

  return (
    <form.AppForm>
      <form.Form label={subject || newMessageTitle} className="min-h-0 flex-1 gap-0">
        {header({
          title: subject || newMessageTitle,
          send: look === "full-screen" ? sendButton : null,
        })}
        <div ref={rowsRef} hidden={collapsed} className="flex min-h-0 flex-1 flex-col">
          <EditorCard look={look} send={look === "window" ? sendButton : null} after={discard}>
            {note}
            <form.AppField name="connectedAccountId">
              {(field) => <field.FromField label="From" accounts={accounts} />}
            </form.AppField>
            <RecipientRows form={form} fields={recipientLists} onTyping={setTypingIn} />
            <form.AppField name="subject">
              {(field) => <field.SubjectField label="Subject" />}
            </form.AppField>
            <form.AppField name="body">
              {(field) => <field.MessageField label="Message" size="fill" />}
            </form.AppField>
            <div className="px-4 empty:hidden">
              <SendFailure
                errors={request.summary(sendErrorMessage)}
                requestId={request.reference}
                error={send.error}
              />
            </div>
          </EditorCard>
        </div>
      </form.Form>
    </form.AppForm>
  );
}
