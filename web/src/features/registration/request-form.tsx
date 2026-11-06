import { useEffect, useRef, type ReactElement } from "react";
import { useForm, useSelector } from "@tanstack/react-form";
import { useQueryClient } from "@tanstack/react-query";
import { getGetRegistrationQueryKey, useRequestRegistrationLink } from "@/api/generated/inbok";
import { NewRegistrationRequest } from "@/api/generated/model";
import { formErrors, formSummary } from "@/api/problem";
import { RequestError } from "@/components/request-error";
import { Button } from "@/components/ui/button";
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { useFocusAfterError } from "@/hooks/use-focus-after-error";
import { blurKeepingSubmitErrors } from "@/lib/blur-keeping-submit-errors";
import { registrationClosed, requestedMessage, requestErrorMessage } from "./visitor-text";

const emptyRequest = { email: "" };
const requestFields = ["email"] as const;
const emailId = "register-email";
const focusClass =
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid";

// RequestForm asks for a sign-up link to an email address, then says to
// check the inbox, whatever the API did with the request.
export function RequestForm(): ReactElement {
  const client = useQueryClient();
  const formRef = useRef<HTMLFormElement>(null);
  const resultRef = useRef<HTMLDivElement>(null);
  const submitRef = useRef<HTMLButtonElement>(null);
  // Set by askAgain: the field takes focus once the form is back.
  const asking = useRef(false);
  const request = useRequestRegistrationLink({
    mutation: {
      gcTime: 0,
      // The page reads availability again and shows registration closed.
      onError: (err) => {
        if (registrationClosed(err))
          void client.invalidateQueries({ queryKey: getGetRegistrationQueryKey() });
      },
    },
  });
  const form = useForm({
    defaultValues: emptyRequest,
    validators: { onChange: NewRegistrationRequest },
    onSubmitInvalid: () => formRef.current?.querySelector<HTMLInputElement>("input")?.focus(),
    onSubmit: ({ value, formApi }) => {
      if (request.isPending) return;
      request.mutate(
        { data: value },
        {
          onError: (err) => {
            const { fields } = formErrors(err, requestFields);
            if (Object.keys(fields).length > 0) formApi.setErrorMap({ onSubmit: { fields } });
          },
        },
      );
    },
    // An idle reset still re-renders the whole form, so only clear an error.
    listeners: {
      onChange: () => {
        if (request.error) request.reset();
      },
    },
  });
  const submitted = useSelector(form.store, (state) => state.submissionAttempts > 0);
  const summary = request.error
    ? formSummary(requestErrorMessage(request.error), formErrors(request.error, requestFields))
    : [];

  // The result replaces the focused form, so it takes focus.
  useEffect(() => {
    if (request.isSuccess) resultRef.current?.focus();
  }, [request.isSuccess]);

  useEffect(() => {
    if (!request.isIdle || !asking.current) return;
    asking.current = false;
    formRef.current?.querySelector<HTMLInputElement>("input")?.focus();
  }, [request.isIdle]);

  useFocusAfterError(request.error, request.isPending, formRef, submitRef);

  // Shows the form again for a new request. The last address stays, so a
  // typo takes one edit.
  function askAgain(): void {
    asking.current = true;
    request.reset();
  }

  if (request.isSuccess)
    return (
      <div ref={resultRef} tabIndex={-1} className="flex flex-col gap-4">
        <p>{requestedMessage(request.variables.data.email)}</p>
        <Button
          type="button"
          variant="outline"
          className={`h-auto min-h-11 w-fit border-muted-foreground whitespace-normal ${focusClass}`}
          onClick={askAgain}
        >
          Use a different email address
        </Button>
      </div>
    );

  return (
    <form
      ref={formRef}
      className="flex flex-col gap-6"
      noValidate
      aria-label="Ask for a sign-up link"
      aria-busy={request.isPending}
      onSubmit={(event) => {
        event.preventDefault();
        void form.handleSubmit();
      }}
    >
      <form.Field name="email">
        {(field) => {
          const errors = field.state.meta.isBlurred || submitted ? field.state.meta.errors : [];
          return (
            <Field data-invalid={errors.length > 0} data-disabled={request.isPending}>
              <FieldLabel htmlFor={emailId}>Email address</FieldLabel>
              <Input
                id={emailId}
                name="email"
                type="email"
                autoComplete="email"
                autoCapitalize="none"
                spellCheck={false}
                className={`h-11 border-muted-foreground dark:aria-invalid:border-destructive ${focusClass}`}
                disabled={request.isPending}
                aria-invalid={errors.length > 0}
                aria-describedby={
                  errors.length > 0
                    ? `${emailId}-description ${emailId}-error`
                    : `${emailId}-description`
                }
                value={field.state.value}
                onBlur={() => blurKeepingSubmitErrors(field)}
                onChange={(event) => field.handleChange(event.target.value)}
              />
              <FieldDescription id={`${emailId}-description`}>
                We&apos;ll email you a link to choose your password.
              </FieldDescription>
              <FieldError id={`${emailId}-error`} errors={errors} />
            </Field>
          );
        }}
      </form.Field>

      <Button
        ref={submitRef}
        type="submit"
        className={`h-auto min-h-11 whitespace-normal ${focusClass}`}
        disabled={request.isPending}
      >
        {request.isPending ? "Sending sign-up link..." : "Send sign-up link"}
      </Button>

      <RequestError errors={summary} requestId={request.error?.requestId} />
    </form>
  );
}
