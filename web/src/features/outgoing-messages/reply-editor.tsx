import { accountDot } from "@/components/account-mark";
import { cn } from "@/lib/cn";
import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactElement } from "react";
import { useForm, useSelector } from "@tanstack/react-form";
import { Trash2Icon } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  getGetOutgoingMessageQueryKey,
  getListConnectedAccountsQueryKey,
  getListOutgoingMessagesQueryKey,
  useReplyToMessage,
} from "@/api/generated/inbok";
import type { ConnectedAccount, OutgoingMessage } from "@/api/generated/model";
import { formErrors, formSummary } from "@/api/problem";
import { repeatableWriteRetry } from "@/api/retry";
import { LeaveWarning } from "@/components/leave-warning";
import { lineLink } from "@/components/link-style";
import { RequestError } from "@/components/request-error";
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
import { FieldError, FieldLabel } from "@/components/ui/field";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Tooltip, TooltipTrigger } from "@/components/ui/tooltip";
import { useFocusAfterError } from "@/hooks/use-focus-after-error";
import { blurKeepingSubmitErrors } from "@/lib/blur-keeping-submit-errors";
import { describedBy, shownErrors, type Submission } from "./field-errors";
import { RecipientInput, withTyping } from "@/components/recipient-input";
import { isAddress } from "./compose-schema";
import type { ReplyMode, ReplyValues } from "./reply-defaults";
import { ReplySchema, replyFields, replyRequest } from "./reply-schema";
import {
  keyReused,
  mayHaveSaved,
  needsReconnect,
  fromOption,
  needsReconnectMark,
  needsReconnectNote,
  otherAccountNote,
  replyErrorMessage,
  replyLabels,
  replyLeaveText,
  sendTooltip,
} from "./text";

const unauthorized = 401;

// The recipient lists, each one row of chips.
const recipientFields = [
  { name: "to", label: "To" },
  { name: "cc", label: "Cc" },
  { name: "bcc", label: "Bcc" },
] as const;

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
  const idPrefix = useId();
  const formRef = useRef<HTMLFormElement>(null);
  const bodyRef = useRef<HTMLTextAreaElement>(null);
  const submitRef = useRef<HTMLButtonElement>(null);
  const [discarding, setDiscarding] = useState(false);
  // The recipient lists holding text not yet a chip, which counts as an
  // edit: Discard asks first, and leaving warns.
  const [typingIn, setTypingIn] = useState<ReadonlySet<string>>(() => new Set());

  // The Cc and Bcc rows shown: those the defaults fill, then those the
  // member adds.
  const [shownLists, setShownLists] = useState<("cc" | "bcc")[]>(() =>
    (["cc", "bcc"] as const).filter((list) => initial[list].length > 0),
  );
  // One key per reply the member means to send, so a repeated request,
  // automatic or by hand, returns the reply the first one saved.
  const [key, setKey] = useState(() => crypto.randomUUID());
  // Set once a try may have saved the reply, as the compose form does.
  const [unconfirmed, setUnconfirmed] = useState(false);
  const send = useReplyToMessage({
    mutation: {
      ...repeatableWriteRetry,
      onError: (err) => {
        if (err.status === unauthorized) onUnauthorized();
        // The account's status has changed since the list was read.
        if (needsReconnect(err))
          void client.invalidateQueries({ queryKey: getListConnectedAccountsQueryKey() });
      },
      // Here, not with each call, so the Outbox learns of a reply saved
      // after the editor closed.
      onSettled: () => client.invalidateQueries({ queryKey: getListOutgoingMessagesQueryKey() }),
    },
    // Each attempt reads the key of the latest render; it changes only
    // after a send has settled. A quoted string, as the IETF
    // Idempotency-Key draft's Structured Field.
    request: { headers: { "Idempotency-Key": `"${key}"` } },
  });

  const form = useForm({
    defaultValues: initial,
    validators: { onChange: ReplySchema },
    onSubmitInvalid: ({ formApi }) => {
      const name = replyFields.find((field) => formApi.getFieldMeta(field)?.errors.length);
      if (name) fieldElement(name)?.focus();
    },
    onSubmit: ({ value, formApi }) => {
      if (send.isPending) return;
      send.mutate(
        { id: answeredId, data: replyRequest(value) },
        {
          onSuccess: (result) => {
            // The status shows at once, without waiting for a read.
            client.setQueryData(getGetOutgoingMessageQueryKey(result.data.id), result);
            onSent(result.data);
          },
          onError: (err) => {
            // The key saved another reply, an earlier try's before the
            // member edited this one: what is sent next is a new reply.
            if (keyReused(err)) setKey(crypto.randomUUID());
            if (mayHaveSaved(err)) setUnconfirmed(true);
            const { fields } = formErrors(err, replyFields);
            if (Object.keys(fields).length > 0) formApi.setErrorMap({ onSubmit: { fields } });
          },
        },
      );
    },
    // After an edit, the last request's message no longer describes the form.
    listeners: {
      onChange: () => {
        if (!send.isIdle) send.reset();
      },
    },
  });
  const submission = useSelector(form.store, (state): Submission =>
    state.submissionAttempts > 0 ? "tried" : "untried",
  );
  const formEdited = useSelector(form.store, (state) => !state.isDefaultValue);
  const edited = formEdited || typingIn.size > 0;
  const summary = send.error
    ? formSummary(replyErrorMessage(send.error), formErrors(send.error, replyFields))
    : [];
  const reconnect = needsReconnect(send.error);
  const receivingCannotSend =
    receiving !== undefined && receiving.status !== "usable" && initial.connectedAccountId === 0;

  useFocusAfterError(send.error, send.isPending, formRef, submitRef);
  useEffect(() => {
    // The recipients are filled in; the member writes the message.
    bodyRef.current?.focus();
  }, []);

  // fieldId returns the id of the input for the field name.
  function fieldId(name: string): string {
    return `${idPrefix}-${name}`;
  }

  // fieldElement returns the control of the field name, to focus.
  function fieldElement(name: (typeof replyFields)[number]): HTMLElement | null | undefined {
    // React Aria names the select's button itself.
    if (name === "connectedAccountId")
      return formRef.current?.querySelector<HTMLElement>("[data-slot=select-trigger]");
    return document.getElementById(fieldId(name));
  }

  // sendOnShortcut sends on Ctrl+Enter, or Cmd+Enter on a Mac, from any
  // text field (DESIGN.md, "Compose and reply").
  function sendOnShortcut(event: KeyboardEvent<HTMLElement>): void {
    if (event.key !== "Enter" || !(event.ctrlKey || event.metaKey)) return;
    event.preventDefault();
    void form.handleSubmit();
  }

  function discard(): void {
    if (edited) setDiscarding(true);
    else onDiscard();
  }

  // showList shows the Cc or Bcc row and moves focus into it.
  function showList(list: "cc" | "bcc"): void {
    setShownLists((lists) => [...lists, list]);
    // After React has drawn the row.
    requestAnimationFrame(() => document.getElementById(fieldId(list))?.focus());
  }

  const chosen = useSelector(form.store, (state) => state.values.connectedAccountId);
  // Gmail threads a reply only within the account that holds the message
  // (ADR006, "Replies"), so another account's reply starts a conversation.
  const otherAccount = receiving !== undefined && chosen !== 0 && chosen !== receiving.id;
  const fromNoteId = fieldId("from-note");

  return (
    <form
      ref={formRef}
      className="flex flex-col gap-3"
      noValidate
      aria-label={replyLabels[mode]}
      aria-busy={send.isPending}
      onSubmit={(event) => {
        event.preventDefault();
        void form.handleSubmit();
      }}
    >
      {/* Inputs are read-only, not disabled, while sending, so focus stays
          and the reply stays readable. */}
      {/* Its outline, not each field's ring, marks the editor in use: a
          text field's caret already shows where typing goes (DESIGN.md
          "Focus"). */}
      <div className="overflow-hidden rounded-xl border bg-card transition-colors focus-within:border-input">
        <form.Field name="connectedAccountId">
          {(field) => {
            const errors = shownErrors(field.state.meta, submission);
            const id = fieldId(field.name);
            const note = receivingCannotSend || otherAccount;
            return (
              <div className="border-b">
                <Select
                  className="flex w-full items-center gap-3 px-4 py-1.5"
                  placeholder="Choose an account"
                  value={field.state.value === 0 ? null : field.state.value}
                  onChange={(value) => field.handleChange(value === null ? 0 : Number(value))}
                  // Its send could only fail; the server refuses it too.
                  disabledKeys={accounts
                    .filter((account) => account.status !== "usable")
                    .map((account) => account.id)}
                  // A select cannot be read-only; a change while sending
                  // would not reach the request anyway.
                  isDisabled={send.isPending}
                  isInvalid={errors.length > 0}
                  aria-describedby={describedBy(id, errors, note ? fromNoteId : undefined)}
                  onBlur={() => blurKeepingSubmitErrors(field)}
                >
                  <FieldLabel className="w-9 shrink-0 font-normal text-muted-foreground">
                    From
                  </FieldLabel>
                  {/* Quiet until pointed at: no outline at rest, a hover
                      tint, then the list; its chevron and label mark it. */}
                  <SelectTrigger
                    size="sm"
                    className="w-auto max-w-full min-w-0 border-transparent bg-transparent px-2 hover:bg-accent data-pressed:bg-accent dark:bg-transparent dark:hover:bg-accent"
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent className="min-w-64">
                    {accounts.map((account) => (
                      <SelectItem key={account.id} id={account.id} textValue={fromOption(account)}>
                        {/* The account's mark, as everywhere it is named
                            (DESIGN.md "Colour"), then its address, which
                            recipients see, and its name, which only the
                            member does. */}
                        <span
                          aria-hidden="true"
                          className={cn(
                            "size-2 shrink-0 self-center rounded-full",
                            accountDot(account.color),
                          )}
                        />
                        <span translate="no">{account.email}</span>
                        {account.name !== null && (
                          <span className="text-muted-foreground">{account.name}</span>
                        )}
                        {account.status !== "usable" && ` ${needsReconnectMark}`}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                {note && (
                  <p id={fromNoteId} className="px-4 pb-2 text-snippet text-muted-foreground">
                    {receivingCannotSend && !otherAccount
                      ? needsReconnectNote(receiving.email)
                      : otherAccountNote}
                  </p>
                )}
                <FieldError id={`${id}-error`} errors={errors} className="px-4 pb-2" />
              </div>
            );
          }}
        </form.Field>

        {recipientFields
          .filter(({ name }) => name === "to" || shownLists.includes(name))
          .map(({ name, label }) => (
            <form.Field key={name} name={name}>
              {(field) => {
                const errors = shownErrors(field.state.meta, submission);
                const id = fieldId(field.name);
                return (
                  <div className="border-b">
                    <div className="flex items-start gap-3 px-4 py-1.5">
                      <Label
                        htmlFor={id}
                        // One line's height, so it centres on the chips'
                        // first line however many lines they wrap to.
                        className="h-8 w-9 shrink-0 font-normal text-muted-foreground pointer-coarse:h-9"
                      >
                        {label}
                      </Label>
                      <RecipientInput
                        id={id}
                        label={label}
                        isAddress={isAddress}
                        value={field.state.value}
                        onChange={(value) => field.handleChange(value)}
                        onBlur={() => blurKeepingSubmitErrors(field)}
                        invalid={errors.length > 0}
                        describedBy={describedBy(id, errors)}
                        readOnly={send.isPending}
                        onKeyDown={sendOnShortcut}
                        onTyping={(state) => setTypingIn((lists) => withTyping(lists, name, state))}
                        className="min-h-8 pointer-coarse:min-h-9"
                      />
                      {name === "to" && (
                        <div className="flex shrink-0 gap-1">
                          {(["cc", "bcc"] as const)
                            .filter((list) => !shownLists.includes(list))
                            .map((list) => (
                              <Button
                                key={list}
                                type="button"
                                variant="ghost"
                                size="sm"
                                aria-label={`Add ${list === "cc" ? "Cc" : "Bcc"} recipients`}
                                onPress={() => showList(list)}
                              >
                                {list === "cc" ? "Cc" : "Bcc"}
                              </Button>
                            ))}
                        </div>
                      )}
                    </div>
                    <FieldError id={`${id}-error`} errors={errors} className="px-4 pb-2 pl-16" />
                  </div>
                );
              }}
            </form.Field>
          ))}

        <form.Field name="body">
          {(field) => {
            const errors = shownErrors(field.state.meta, submission);
            const id = fieldId(field.name);
            return (
              <div>
                <textarea
                  ref={bodyRef}
                  id={id}
                  name={field.name}
                  aria-label="Message"
                  readOnly={send.isPending}
                  aria-invalid={errors.length > 0}
                  aria-describedby={describedBy(id, errors)}
                  value={field.state.value}
                  onBlur={() => blurKeepingSubmitErrors(field)}
                  onChange={(event) => field.handleChange(event.target.value)}
                  onKeyDown={sendOnShortcut}
                  className="block min-h-44 w-full resize-y bg-transparent px-4 py-3 text-message outline-none"
                />
                <FieldError id={`${id}-error`} errors={errors} className="px-4 pb-2" />
              </div>
            );
          }}
        </form.Field>

        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 border-t px-3 py-2">
          {/* NOTE: React Aria's Button drops aria-keyshortcuts, so the
              tooltip, its description, is what tells assistive technology
              the shortcut. */}
          <TooltipTrigger>
            {/* Disabled while sending, so a second click or Enter sends nothing. */}
            <Button ref={submitRef} type="submit" isDisabled={send.isPending}>
              {send.isPending ? "Sending..." : "Send"}
            </Button>
            <Tooltip>{sendTooltip()}</Tooltip>
          </TooltipTrigger>
          <TooltipTrigger>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label="Discard"
              isDisabled={send.isPending}
              onPress={discard}
            >
              <Trash2Icon aria-hidden="true" />
            </Button>
            <Tooltip>Discard</Tooltip>
          </TooltipTrigger>
        </div>
      </div>

      <RequestError errors={summary} requestId={send.error?.requestId}>
        {/* A new tab lets the member check without losing the reply here. */}
        {keyReused(send.error) && (
          <p>
            <Link to="/mail/outbox" target="_blank" className={lineLink}>
              Open the Outbox in a new tab
            </Link>
          </p>
        )}
        {reconnect && (
          <p>
            <Link to="/settings/connected-accounts" className={lineLink}>
              Open Connected accounts
            </Link>
          </p>
        )}
      </RequestError>

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
        active={edited || send.isPending || unconfirmed}
        text={replyLeaveText(send.isPending ? "sending" : unconfirmed ? "unconfirmed" : "typed")}
      />
    </form>
  );
}
