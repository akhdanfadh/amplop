import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactElement,
  type ReactNode,
} from "react";
import { revalidateLogic, useSelector } from "@tanstack/react-form";
import { Trash2Icon } from "lucide-react";
import { useSendMessage, useSendMessageAgain } from "@/api/generated/inbok";
import type { ListedAccount, OutgoingMessage } from "@/api/generated/model";
import { useAppForm } from "@/components/form/app-form";
import { useFormRequest } from "@/components/form/request";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipTrigger } from "@/components/ui/tooltip";
import { FixedRow } from "@/components/form/fields";
import { inlineLinkButton } from "@/components/link-style";
import { accountDot } from "@/components/account-mark";
import { cn } from "@/lib/cn";
import {
  ComposeSchema,
  composeFields,
  composeRequest,
  editRequest,
  type ComposeValues,
} from "./compose-schema";
import {
  EditNote,
  EditorCard,
  RecipientRows,
  SendFailure,
  recipientLists,
  useEditorSend,
} from "./editor";
import {
  newMessageTitle,
  sendAgainErrorMessage,
  sendErrorMessage,
  showAllSubject,
  type LeaveState,
} from "./text";

// The parts of the compose window's top bar that the form owns: title, the
// window's name, which follows the subject; and send, the Send button when
// the look puts it in the top bar, else null.
export type ComposeHeader = { title: string; send: ReactNode };

// ComposeForm writes a new message and sends it from one of accounts, the
// member's connected accounts, oldest first; one that isn't usable needs
// reconnecting and can't be chosen. Or with edit, a message Gmail refused,
// it sends that message again with the member's edits, from its own
// account and, for a reply, with its own subject, both then fixed rows.
// initial is what the form starts with; note, shown above the rows, e.g.
// the signed-out banner while the window covers the page. look is the compose
// window's: "window" puts Send in the bar under the message, "full-screen"
// in header, the window's top bar, which the form draws first. collapsed
// hides all but header while the window is minimised, keeping what is
// typed. onSent receives the saved message, whose send goes on in the
// background; onDiscard runs on Discard; onLoses receives what closing the
// window would lose, whenever that changes.
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
  edit,
}: {
  edit?: OutgoingMessage;
  accounts: ListedAccount[];
  initial: ComposeValues;
  note?: ReactNode;
  look: "window" | "full-screen";
  header: (parts: ComposeHeader) => ReactNode;
  collapsed: boolean;
  onSent: (message: OutgoingMessage) => void;
  onDiscard: () => void;
  onLoses: (loses: LeaveState | null) => void;
}): ReactElement {
  const rowsRef = useRef<HTMLDivElement>(null);
  const editor = useEditorSend();
  // Both hooks run, as hooks must; edit, fixed for the form's life, picks
  // the one that sends. Send again needs no key: the message is its own.
  const create = useSendMessage(editor.options);
  const again = useSendMessageAgain({ mutation: editor.options.mutation });
  const createRequest = useFormRequest(create, composeFields);
  const againRequest = useFormRequest(again, composeFields);
  const send = edit ? again : create;
  const request = edit ? againRequest : createRequest;
  const form = useAppForm({
    defaultValues: initial,
    validationLogic: revalidateLogic(),
    validators: { onDynamic: request.check(ComposeSchema) },
    onSubmit: async ({ value, formApi }) => {
      const sent = edit
        ? await againRequest.send(formApi, value, { id: edit.id, data: editRequest(edit, value) })
        : await createRequest.send(formApi, value, { data: composeRequest(value) });
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
            {edit ? (
              <>
                <EditNote />
                <FixedRow label="From">
                  <EditedFrom message={edit} accounts={accounts} />
                </FixedRow>
              </>
            ) : (
              <form.AppField name="connectedAccountId">
                {(field) => <field.FromField label="From" accounts={accounts} />}
              </form.AppField>
            )}
            <RecipientRows form={form} fields={recipientLists} onTyping={setTypingIn} />
            {edit?.reply ? (
              <FixedRow label="Subject">
                <FixedSubject subject={edit.subject} />
              </FixedRow>
            ) : (
              <form.AppField name="subject">
                {(field) => <field.SubjectField label="Subject" />}
              </form.AppField>
            )}
            <form.AppField name="body">
              {(field) => <field.MessageField label="Message" size="fill" />}
            </form.AppField>
            <div className="px-4 empty:hidden">
              <SendFailure
                errors={request.summary(edit ? sendAgainErrorMessage : sendErrorMessage)}
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

// EditedFrom is the account message goes from again, as From names an
// account: its mark, then its address and its name as one line of text, so
// a long pair wraps between them before the address breaks; its saved
// address while the accounts don't list it.
export function EditedFrom({
  message,
  accounts,
}: {
  message: Pick<OutgoingMessage, "connectedAccountId" | "from">;
  accounts: readonly ListedAccount[];
}): ReactElement {
  const account = accounts.find((one) => one.id === message.connectedAccountId);
  return (
    <>
      {account && (
        // On the first line's middle: the row's 20 px line, less the mark.
        <span
          aria-hidden="true"
          className={cn("mt-1.5 size-2 shrink-0 rounded-full", accountDot(account.color))}
        />
      )}
      <span className="min-w-0">
        <span translate="no">{account?.email ?? message.from}</span>
        {account?.name != null && (
          <>
            {" "}
            <span className="text-muted-foreground">{account.name}</span>
          </>
        )}
      </span>
    </>
  );
}

// FixedSubject is a refused reply's subject, which sending it again keeps:
// at most 2 lines, then Show all, which unfolds it and takes focus to it,
// as a message's details do for a long list of addresses.
//
// DESIGN: a reply's subject can run to 500 characters, which would push
// the message out of the window; 2 lines name the conversation.
function FixedSubject({ subject }: { subject: string }): ReactElement {
  const text = useRef<HTMLSpanElement>(null);
  const [all, setAll] = useState(false);
  const [cut, setCut] = useState(false);
  // Set by Show all, so focus moves only after the member asked.
  const revealed = useRef(false);

  useLayoutEffect(() => {
    const element = text.current;
    if (!element || all) return undefined;
    // Cut when its text is taller than the 2 lines shown, which a width
    // change can alter.
    const measure = (): void => setCut(element.scrollHeight > element.clientHeight);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, [all]);
  useEffect(() => {
    if (!all || !revealed.current) return;
    revealed.current = false;
    // The button that had focus is gone.
    text.current?.focus();
  }, [all]);

  return (
    <span className="min-w-0">
      <span
        ref={text}
        tabIndex={-1}
        className={cn("rounded-sm outline-none focus-visible:focus-ring", !all && "line-clamp-2")}
      >
        {subject}
      </span>
      {!all && cut && (
        <Button
          variant="link"
          size="sm"
          className={inlineLinkButton}
          onPress={() => {
            revealed.current = true;
            setAll(true);
          }}
        >
          {showAllSubject}
        </Button>
      )}
    </span>
  );
}
