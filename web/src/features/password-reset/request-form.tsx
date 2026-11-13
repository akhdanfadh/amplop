import { useEffect, useRef, type ReactElement } from "react";
import { useForm, useSelector } from "@tanstack/react-form";
import { useRequestPasswordResetLink } from "@/api/generated/inbok";
import { NewPasswordResetRequest } from "@/api/generated/model";
import { emailAddress } from "@/api/validation";
import { formErrors, formSummary } from "@/api/problem";
import { RequestError } from "@/components/request-error";
import { Button } from "@/components/ui/button";
import { Field, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { useFocusAfterError } from "@/hooks/use-focus-after-error";
import { blurKeepingSubmitErrors } from "@/lib/blur-keeping-submit-errors";
import { requestedMessage, requestErrorMessage } from "./text";

const emptyRequest = { email: "" };
const requestFields = ["email"] as const;
const emailId = "reset-request-email";

// RequestForm asks for a reset link to an email address, then gives the one
// answer every address gets.
export function RequestForm(): ReactElement {
  const formRef = useRef<HTMLFormElement>(null);
  const resultRef = useRef<HTMLDivElement>(null);
  const submitRef = useRef<HTMLButtonElement>(null);
  // Set by askAgain: the field takes focus once the form is back.
  const asking = useRef(false);
  const request = useRequestPasswordResetLink({ mutation: { gcTime: 0 } });
  const form = useForm({
    defaultValues: emptyRequest,
    validators: {
      onChange: NewPasswordResetRequest.extend({
        email: emailAddress(NewPasswordResetRequest.shape.email),
      }),
    },
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

  // The answer replaces the focused form, so it takes focus.
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
          className="w-fit whitespace-normal"
          onPress={askAgain}
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
      aria-label="Ask for a reset link"
      aria-busy={request.isPending}
      onSubmit={(event) => {
        event.preventDefault();
        void form.handleSubmit();
      }}
    >
      <p>
        Enter the email address you sign in with. If it is verified, we&apos;ll email you a link to
        choose a new password.
      </p>
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
                // The address an account signs in with, which a password
                // manager offers as the saved login.
                autoComplete="username"
                autoCapitalize="none"
                spellCheck={false}
                disabled={request.isPending}
                aria-invalid={errors.length > 0}
                aria-describedby={errors.length > 0 ? `${emailId}-error` : undefined}
                value={field.state.value}
                onBlur={() => blurKeepingSubmitErrors(field)}
                onChange={(event) => field.handleChange(event.target.value)}
              />
              <FieldError id={`${emailId}-error`} errors={errors} />
            </Field>
          );
        }}
      </form.Field>

      <Button
        ref={submitRef}
        type="submit"
        className="whitespace-normal"
        isDisabled={request.isPending}
      >
        {request.isPending ? "Sending reset link..." : "Send reset link"}
      </Button>

      <RequestError errors={summary} requestId={request.error?.requestId} />
    </form>
  );
}
