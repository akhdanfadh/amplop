import { useRef, useState, type ReactElement } from "react";
import { useForm, useSelector, type AnyFieldMeta } from "@tanstack/react-form";
import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  getListConnectedAccountsQueryKey,
  getListOutgoingMessagesQueryKey,
  useSendMessage,
} from "@/api/generated/inbok";
import type { ConnectedAccount, OutgoingMessage } from "@/api/generated/model";
import { formErrors, formSummary } from "@/api/problem";
import { repeatableWriteRetry } from "@/api/retry";
import { LeaveWarning } from "@/components/leave-warning";
import { RequestError } from "@/components/request-error";
import { Button } from "@/components/ui/button";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { Textarea } from "@/components/ui/textarea";
import { useFocusAfterError } from "@/hooks/use-focus-after-error";
import { blurKeepingSubmitErrors } from "@/lib/blur-keeping-submit-errors";
import { ComposeSchema, composeFields, composeRequest, type ComposeValues } from "./compose-schema";
import { keyReused, leaveText, mayHaveSaved, needsReconnect, sendErrorMessage } from "./text";

const unauthorized = 401;
const focusClass =
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid";
const inputClass = `h-11 border-muted-foreground dark:aria-invalid:border-destructive ${focusClass}`;
const linkClass = `inline-flex min-h-11 items-center underline underline-offset-4 ${focusClass}`;

// The recipient lists, each one text field.
const recipientFields = [
  { name: "to", label: "To" },
  { name: "cc", label: "Cc" },
  { name: "bcc", label: "Bcc" },
] as const;

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
  const client = useQueryClient();
  const formRef = useRef<HTMLFormElement>(null);
  const submitRef = useRef<HTMLButtonElement>(null);
  // One key per message the member means to send, so a repeated request,
  // automatic or by hand, returns the message the first one saved.
  // crypto.randomUUID needs a secure context, which signing in already
  // does: the session cookie is Secure.
  const [key, setKey] = useState(() => crypto.randomUUID());
  // Set once a try may have saved the message, and kept through later
  // edits and sends, which reset the request's error: only the Outbox can
  // tell whether it was saved.
  const [unconfirmed, setUnconfirmed] = useState(false);
  const send = useSendMessage({
    mutation: {
      ...repeatableWriteRetry,
      onError: (err) => {
        if (err.status === unauthorized) onUnauthorized();
        // The account's status has changed since the list was read.
        if (needsReconnect(err))
          void client.invalidateQueries({ queryKey: getListConnectedAccountsQueryKey() });
      },
      // Here, not with each call, so the Outbox learns of a message saved
      // after the member left the page.
      onSettled: () => client.invalidateQueries({ queryKey: getListOutgoingMessagesQueryKey() }),
    },
    // Each attempt reads the key of the latest render, so the key changes
    // only after a send has settled, below; never while one is pending.
    // A quoted string, as the IETF Idempotency-Key draft's Structured Field.
    request: { headers: { "Idempotency-Key": `"${key}"` } },
  });

  const form = useForm({
    defaultValues: initial,
    validators: { onChange: ComposeSchema },
    onSubmitInvalid: ({ formApi }) => {
      const name = composeFields.find((field) => formApi.getFieldMeta(field)?.errors.length);
      if (name) document.getElementById(fieldId(name))?.focus();
    },
    onSubmit: ({ value, formApi }) => {
      if (send.isPending) return;
      send.mutate(
        { data: composeRequest(value) },
        {
          // Here rather than on the hook, whose callbacks still run after
          // the page has closed and would take the member back to it.
          onSuccess: (result) => onSent(result.data),
          onError: (err) => {
            // The key saved another message, an earlier try's before the
            // member edited this one: what is sent next is a new message.
            if (keyReused(err)) setKey(crypto.randomUUID());
            if (mayHaveSaved(err)) setUnconfirmed(true);
            const { fields } = formErrors(err, composeFields);
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
  const submitted = useSelector(form.store, (state) => state.submissionAttempts > 0);
  const edited = useSelector(form.store, (state) => !state.isDefaultValue);
  const summary = send.error
    ? formSummary(sendErrorMessage(send.error), formErrors(send.error, composeFields))
    : [];
  const reconnect = needsReconnect(send.error);

  useFocusAfterError(send.error, send.isPending, formRef, submitRef);

  return (
    <form
      ref={formRef}
      className="flex flex-col gap-6"
      noValidate
      aria-label="New message"
      aria-busy={send.isPending}
      onSubmit={(event) => {
        event.preventDefault();
        void form.handleSubmit();
      }}
    >
      {/* Inputs are read-only, not disabled, while sending, so focus stays
          and the message stays readable. */}
      <FieldGroup>
        <form.Field name="connectedAccountId">
          {(field) => {
            const errors = shown(field.state.meta, submitted);
            const id = fieldId(field.name);
            return (
              <Field data-invalid={errors.length > 0}>
                <FieldLabel htmlFor={id}>From</FieldLabel>
                <NativeSelect
                  id={id}
                  name={field.name}
                  className={inputClass}
                  // A select cannot be read-only; a change while sending
                  // would not reach the request anyway.
                  disabled={send.isPending}
                  aria-invalid={errors.length > 0}
                  aria-describedby={describedBy(id, errors, `${id}-description`)}
                  value={field.state.value}
                  onBlur={() => blurKeepingSubmitErrors(field)}
                  onChange={(event) => field.handleChange(Number(event.target.value))}
                >
                  {/* Shown until the member chooses: no account is preselected. */}
                  {field.state.value === 0 && (
                    <NativeSelectOption value={0} disabled>
                      Choose an account
                    </NativeSelectOption>
                  )}
                  {accounts.map((account) => (
                    <NativeSelectOption
                      key={account.id}
                      value={account.id}
                      // Its send could only fail; the server refuses it too.
                      disabled={account.status !== "usable"}
                    >
                      {account.email}
                      {account.status !== "usable" && " (needs reconnecting)"}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
                <FieldDescription id={`${id}-description`}>
                  Recipients see this address. It sends through Gmail.
                </FieldDescription>
                <FieldError id={`${id}-error`} errors={errors} />
              </Field>
            );
          }}
        </form.Field>

        {recipientFields.map(({ name, label }) => (
          <form.Field key={name} name={name}>
            {(field) => {
              const errors = shown(field.state.meta, submitted);
              const id = fieldId(field.name);
              return (
                <Field data-invalid={errors.length > 0}>
                  <FieldLabel htmlFor={id}>{label}</FieldLabel>
                  <Input
                    id={id}
                    name={field.name}
                    className={inputClass}
                    // Not type="email" multiple: browsers rewrite its value
                    // while typing, dropping the space after a comma.
                    type="text"
                    inputMode="email"
                    autoComplete="off"
                    autoCapitalize="none"
                    spellCheck={false}
                    readOnly={send.isPending}
                    aria-invalid={errors.length > 0}
                    aria-describedby={describedBy(
                      id,
                      errors,
                      name === "to" ? `${id}-description` : undefined,
                    )}
                    value={field.state.value}
                    onBlur={() => blurKeepingSubmitErrors(field)}
                    onChange={(event) => field.handleChange(event.target.value)}
                  />
                  {name === "to" && (
                    <FieldDescription id={`${id}-description`}>
                      Email addresses only, e.g. ana@example.com, separated by commas.
                    </FieldDescription>
                  )}
                  <FieldError id={`${id}-error`} errors={errors} />
                </Field>
              );
            }}
          </form.Field>
        ))}

        <form.Field name="subject">
          {(field) => {
            const errors = shown(field.state.meta, submitted);
            const id = fieldId(field.name);
            return (
              <Field data-invalid={errors.length > 0}>
                <FieldLabel htmlFor={id}>Subject</FieldLabel>
                <Input
                  id={id}
                  name={field.name}
                  className={inputClass}
                  readOnly={send.isPending}
                  aria-invalid={errors.length > 0}
                  aria-describedby={describedBy(id, errors)}
                  value={field.state.value}
                  onBlur={() => blurKeepingSubmitErrors(field)}
                  onChange={(event) => field.handleChange(event.target.value)}
                />
                <FieldError id={`${id}-error`} errors={errors} />
              </Field>
            );
          }}
        </form.Field>

        <form.Field name="body">
          {(field) => {
            const errors = shown(field.state.meta, submitted);
            const id = fieldId(field.name);
            return (
              <Field data-invalid={errors.length > 0}>
                <FieldLabel htmlFor={id}>Message</FieldLabel>
                <Textarea
                  id={id}
                  name={field.name}
                  className={`min-h-60 border-muted-foreground dark:aria-invalid:border-destructive ${focusClass}`}
                  readOnly={send.isPending}
                  aria-invalid={errors.length > 0}
                  aria-describedby={describedBy(id, errors, `${id}-description`)}
                  value={field.state.value}
                  onBlur={() => blurKeepingSubmitErrors(field)}
                  onChange={(event) => field.handleChange(event.target.value)}
                />
                <FieldDescription id={`${id}-description`}>Sent as plain text.</FieldDescription>
                <FieldError id={`${id}-error`} errors={errors} />
              </Field>
            );
          }}
        </form.Field>
      </FieldGroup>

      {/* Disabled while sending, so a second click or Enter sends nothing. */}
      <Button
        ref={submitRef}
        type="submit"
        className={`h-auto min-h-11 w-fit whitespace-normal ${focusClass}`}
        disabled={send.isPending}
      >
        {send.isPending ? "Sending..." : "Send"}
      </Button>

      <RequestError errors={summary} requestId={send.error?.requestId}>
        {/* A new tab lets the member check without losing the edits here. */}
        {keyReused(send.error) && (
          <p>
            <Link to="/outbox" target="_blank" className={linkClass}>
              Open the Outbox in a new tab
            </Link>
          </p>
        )}
        {reconnect && (
          <p>
            <Link to="/connected-accounts" className={linkClass}>
              Open Connected accounts
            </Link>
          </p>
        )}
      </RequestError>
      <LeaveWarning
        active={edited || send.isPending || unconfirmed}
        text={leaveText(send.isPending ? "sending" : unconfirmed ? "unconfirmed" : "typed")}
      />
    </form>
  );
}

// fieldId returns the id of the input for the form field name.
function fieldId(name: string): string {
  return `compose-${name}`;
}

// describedBy returns what describes the input with id: the element with
// descriptionId, if given, and its error when errors has one.
function describedBy(
  id: string,
  errors: AnyFieldMeta["errors"],
  descriptionId?: string,
): string | undefined {
  const ids = [descriptionId, errors.length > 0 ? `${id}-error` : undefined];
  return ids.filter(Boolean).join(" ") || undefined;
}

// A field's errors once the member has left it or tried to send, so an
// address is not flagged while it is being typed.
function shown(meta: AnyFieldMeta, submitted: boolean): AnyFieldMeta["errors"] {
  return meta.isBlurred || submitted ? meta.errors : [];
}
