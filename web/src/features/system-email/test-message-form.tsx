import { useEffect, useRef, type ReactElement } from "react";
import { useForm, useSelector } from "@tanstack/react-form";
import { useMutationState, useQueryClient } from "@tanstack/react-query";
import {
  getGetSystemEmailQueryKey,
  getSaveSystemEmailMutationKey,
  useSendSystemEmailTestMessage,
} from "@/api/generated/inbok";
import { SystemEmailTestMessageRecipient, type SystemEmailSettings } from "@/api/generated/model";
import { formErrors, formSummary, uncertainOutcome } from "@/api/problem";
import { ApiError } from "@/api/transport";
import { Button } from "@/components/ui/button";
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { blurKeepingSubmitErrors } from "@/lib/blur-keeping-submit-errors";
import { testErrorMessage, testResultMessage } from "./test-result";

const recipientFields = ["recipient"] as const;
const id = "system-email-recipient";
const statusId = "system-email-test-status";

const testedFormat = new Intl.DateTimeFormat(undefined, {
  dateStyle: "medium",
  timeStyle: "short",
});

// TestMessageForm sends a test message with the saved settings, absent
// before the first save. recipient is the address to start with; savedAt is
// when a save last may have replaced the settings, in milliseconds;
// onAccessError receives every test error.
export function TestMessageForm({
  settings,
  recipient,
  savedAt,
  onAccessError,
}: {
  settings: SystemEmailSettings | undefined;
  recipient: string;
  savedAt: number;
  onAccessError: (err: ApiError) => void;
}): ReactElement {
  const client = useQueryClient();
  const buttonRef = useRef<HTMLButtonElement>(null);
  // Set when a test starts; focus moves once it ends.
  const refocus = useRef(false);
  // A test uses the saved settings, so it waits while a save is running or
  // its outcome is unknown.
  const saveUnsettled = useMutationState({
    filters: { mutationKey: getSaveSystemEmailMutationKey() },
    select: ({ state }) =>
      state.status === "pending" ||
      (state.error instanceof ApiError && uncertainOutcome(state.error)),
  }).some(Boolean);
  const send = useSendSystemEmailTestMessage({
    mutation: {
      onError: onAccessError,
      // A pass records the tested time and a failure clears it; a lost
      // response may have done either.
      onSettled: () => client.invalidateQueries({ queryKey: getGetSystemEmailQueryKey() }),
    },
  });
  const locked = send.isPending || saveUnsettled || !settings;

  const form = useForm({
    defaultValues: { recipient },
    validators: { onChange: SystemEmailTestMessageRecipient },
    onSubmitInvalid: () => document.getElementById(id)?.focus(),
    onSubmit: ({ value, formApi }) => {
      if (locked) return;
      refocus.current = true;
      send.mutate(
        { data: value },
        {
          onError: (err) => {
            const { fields } = formErrors(err, recipientFields);
            if (Object.keys(fields).length > 0) formApi.setErrorMap({ onSubmit: { fields } });
          },
        },
      );
    },
    listeners: {
      onChange: () => {
        if (!send.isIdle) send.reset();
      },
    },
  });
  const submitted = useSelector(form.store, (state) => state.submissionAttempts > 0);
  // A result describes the settings it tested, so a later save hides it.
  const current = send.submittedAt > savedAt;
  const result =
    current && send.data && send.variables
      ? { ...send.data.data, recipient: send.variables.data.recipient }
      : undefined;
  const error = current ? send.error : null;
  const failed = result?.status === "failed";
  const summary = error
    ? formSummary(testErrorMessage(error), formErrors(error, recipientFields))
    : [];

  // The button is disabled while sending; give focus back once it ends.
  useEffect(() => {
    if (!refocus.current || send.isPending) return;
    refocus.current = false;
    if (form.getFieldMeta("recipient")?.errors.length) document.getElementById(id)?.focus();
    else buttonRef.current?.focus();
  });

  return (
    <form
      className="flex flex-col gap-6"
      noValidate
      aria-label="Send a test message"
      aria-busy={send.isPending}
      onSubmit={(event) => {
        event.preventDefault();
        void form.handleSubmit();
      }}
    >
      <p id={statusId} className="font-medium">
        <TestedStatus settings={settings} />
      </p>

      <form.Field name="recipient">
        {(field) => {
          const errors = field.state.meta.isBlurred || submitted ? field.state.meta.errors : [];
          return (
            <Field data-invalid={errors.length > 0}>
              <FieldLabel htmlFor={id}>Recipient</FieldLabel>
              <Input
                id={id}
                name={field.name}
                className="h-11 border-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid dark:aria-invalid:border-destructive"
                type="email"
                autoComplete="email"
                autoCapitalize="none"
                spellCheck={false}
                readOnly={locked}
                aria-invalid={errors.length > 0}
                aria-describedby={`${id}-description ${statusId}${errors.length > 0 ? ` ${id}-error` : ""}`}
                value={field.state.value}
                onBlur={() => blurKeepingSubmitErrors(field)}
                onChange={(event) => field.handleChange(event.target.value)}
              />
              <FieldDescription id={`${id}-description`}>
                Where to send the test message.
              </FieldDescription>
              <FieldError id={`${id}-error`} errors={errors} />
            </Field>
          );
        }}
      </form.Field>

      <Button
        ref={buttonRef}
        type="submit"
        variant="outline"
        className="h-auto min-h-11 w-fit border-muted-foreground whitespace-normal focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
        disabled={locked}
      >
        {send.isPending ? "Sending test message..." : "Send test message"}
      </Button>

      {error && summary.length > 0 && (
        <div className="flex flex-col gap-2">
          <FieldError errors={summary} />
          {error.requestId && (
            <p className="text-sm text-muted-foreground">
              Request reference: <code className="break-all">{error.requestId}</code>
            </p>
          )}
        </div>
      )}
      <output aria-atomic="true" className={failed ? "text-destructive" : undefined}>
        {result && testResultMessage(result, result.recipient)}
      </output>
    </form>
  );
}

// TestedStatus says whether the saved settings have passed a test.
function TestedStatus({ settings }: { settings: SystemEmailSettings | undefined }): ReactElement {
  if (!settings) return <>Save the settings before sending a test message.</>;
  if (!settings.testedAt) return <>These settings haven&apos;t passed a test yet.</>;
  return (
    <>
      Last test passed{" "}
      <time dateTime={settings.testedAt}>{testedFormat.format(new Date(settings.testedAt))}</time>.
    </>
  );
}
