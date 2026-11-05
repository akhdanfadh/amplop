import { useEffect, useRef, useState, type ReactElement } from "react";
import { useForm, useSelector } from "@tanstack/react-form";
import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useRedeemPasswordResetLink } from "@/api/generated/inbok";
import {
  redeemPasswordResetLinkBodyPasswordMax,
  redeemPasswordResetLinkBodyPasswordMin,
} from "@/api/generated/model";
import { formErrors, formSummary, uncertainOutcome } from "@/api/problem";
import { PasswordInput } from "@/components/password-input";
import { Button } from "@/components/ui/button";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { useFocusAfterError } from "@/hooks/use-focus-after-error";
import { blurKeepingSubmitErrors } from "@/lib/blur-keeping-submit-errors";
import { clearAccountCache } from "@/features/auth/session-cache";
import { ResetSchema } from "./reset-schema";
import { completeErrorMessage, endOf, linkInvalid, type EarlierTries, type Ended } from "./text";

const emptyValues = { email: "", password: "", confirmPassword: "" };
const emailId = "reset-email";
const focusClass =
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid";
const linkClass = `inline-flex min-h-11 w-fit items-center underline underline-offset-4 ${focusClass}`;

const inputs = [
  {
    name: "password",
    label: "New password",
    description: `Use ${redeemPasswordResetLinkBodyPasswordMin} to ${redeemPasswordResetLinkBodyPasswordMax} characters.`,
    toggle: "new password",
  },
  {
    name: "confirmPassword",
    label: "Confirm new password",
    description: "Enter the same password again.",
    toggle: "new password confirmation",
  },
] as const;
const resetFields = ["email", ...inputs.map((input) => input.name)] as const;

// ResetForm sets a new password from secret, the text after # in a reset
// link, the email the visitor types, which must be the one the link was sent
// to, and the password they choose. onDone receives the account's email, for
// signing in.
export function ResetForm({
  secret,
  onDone,
}: {
  secret: string;
  onDone: (email: string) => void;
}): ReactElement {
  const formRef = useRef<HTMLFormElement>(null);
  const submitRef = useRef<HTMLButtonElement>(null);
  const endRef = useRef<HTMLDivElement>(null);
  // A try whose outcome is unknown may have changed the password.
  const earlier = useRef<EarlierTries>("refused");
  const [ended, setEnded] = useState<Ended>();
  const queryClient = useQueryClient();
  const redeem = useRedeemPasswordResetLink({
    mutation: {
      // Do not keep the chosen password after the form unmounts.
      gcTime: 0,
      // A reset signs the account out everywhere, this browser too, so its
      // cached data goes, even when the answer arrives after the form
      // unmounted. A result that may have reset counts as one.
      onSuccess: () => clearAccountCache(queryClient),
      onError: (err) => {
        if (uncertainOutcome(err)) clearAccountCache(queryClient);
      },
    },
  });
  const form = useForm({
    defaultValues: emptyValues,
    validators: { onChange: ResetSchema },
    onSubmitInvalid: ({ formApi }) => {
      const name = resetFields.find((field) => formApi.getFieldMeta(field)?.errors.length);
      if (name) formRef.current?.querySelector<HTMLInputElement>(`[name="${name}"]`)?.focus();
    },
    onSubmit: ({ value, formApi }) => {
      if (redeem.isPending) return;
      redeem.mutate(
        { data: { secret, email: value.email, password: value.password } },
        {
          onSuccess: (result) => {
            formApi.reset();
            onDone(result.data.email);
          },
          onError: (err) => {
            // 1. A link that no longer works ends the page.
            if (linkInvalid(err)) {
              setEnded(endOf(err, earlier.current));
              // The end screen needs neither the password nor the secret.
              formApi.reset();
              redeem.reset();
              return;
            }

            // 2. Remember a try that may have reset, for the end's wording.
            if (uncertainOutcome(err)) earlier.current = "unknown";
            // 3. Mark the fields the refusal names.
            const { fields } = formErrors(err, resetFields);
            if (Object.keys(fields).length > 0) formApi.setErrorMap({ onSubmit: { fields } });
          },
        },
      );
    },
    // An idle reset still re-renders the whole form, so only clear an error.
    listeners: {
      onChange: () => {
        if (redeem.error) redeem.reset();
      },
    },
  });
  const submitted = useSelector(form.store, (state) => state.submissionAttempts > 0);
  const summary = redeem.error
    ? formSummary(completeErrorMessage(redeem.error), formErrors(redeem.error, resetFields))
    : [];

  useFocusAfterError(redeem.error, redeem.isPending, formRef, submitRef);

  // The end replaces the focused form, so it takes focus.
  useEffect(() => {
    if (ended) endRef.current?.focus();
  }, [ended]);

  // No retry with this link can work, so the form goes.
  if (ended)
    return (
      <div ref={endRef} tabIndex={-1} className="flex flex-col gap-4">
        <p>{ended.message}</p>
        {ended.links.map((link) =>
          link === "new-link" ? (
            <Link key={link} to="/forgot-password" className={linkClass}>
              Ask for a new link
            </Link>
          ) : (
            <Link key={link} to="/sign-in" className={linkClass}>
              Sign in
            </Link>
          ),
        )}
      </div>
    );

  return (
    <form
      ref={formRef}
      className="flex flex-col gap-6"
      noValidate
      aria-label="Choose a new password"
      aria-busy={redeem.isPending}
      onSubmit={(event) => {
        event.preventDefault();
        void form.handleSubmit();
      }}
    >
      <div className="flex flex-col gap-3">
        <p>
          Enter the email address this link was sent to and choose a new password. Changing it signs
          your account out on every browser.
        </p>
        <p>If you sign in with an authenticator app, you&apos;ll still need its code.</p>
      </div>
      <FieldGroup>
        <form.Field name="email">
          {(field) => {
            const errors = field.state.meta.isBlurred || submitted ? field.state.meta.errors : [];
            // Only the server's refusal is a submit error, almost always
            // because the link was sent to another email; only a link to
            // that address can reset its password. Reading the field, not
            // the request, keeps the link exactly as long as the error.
            const refused = field.state.meta.errorMap.onSubmit !== undefined;
            return (
              <Field data-invalid={errors.length > 0} data-disabled={redeem.isPending}>
                <FieldLabel htmlFor={emailId}>Email address</FieldLabel>
                <Input
                  id={emailId}
                  name="email"
                  type="email"
                  // "username" lets a password manager save it with the new
                  // password as one login.
                  autoComplete="username"
                  autoCapitalize="none"
                  spellCheck={false}
                  className={`h-11 border-muted-foreground dark:aria-invalid:border-destructive ${focusClass}`}
                  disabled={redeem.isPending}
                  aria-invalid={errors.length > 0}
                  aria-describedby={errors.length > 0 ? `${emailId}-error` : undefined}
                  value={field.state.value}
                  onBlur={() => blurKeepingSubmitErrors(field)}
                  onChange={(event) => field.handleChange(event.target.value)}
                />
                <FieldError id={`${emailId}-error`} errors={errors} />
                {refused && (
                  // Field stretches its children; the paragraph keeps the
                  // link's focus outline to its text. An action, not part of
                  // the error, so not in its color.
                  <p>
                    <Link to="/forgot-password" className={`text-foreground ${linkClass}`}>
                      Ask for a new link
                    </Link>
                  </p>
                )}
              </Field>
            );
          }}
        </form.Field>
        {inputs.map(({ name, label, description, toggle }) => (
          <form.Field key={name} name={name}>
            {(field) => {
              const errors = field.state.meta.isBlurred || submitted ? field.state.meta.errors : [];
              const id = `reset-${name}`;
              return (
                <Field data-invalid={errors.length > 0} data-disabled={redeem.isPending}>
                  <FieldLabel htmlFor={id}>{label}</FieldLabel>
                  <PasswordInput
                    label={toggle}
                    id={id}
                    name={name}
                    type="password"
                    autoComplete="new-password"
                    disabled={redeem.isPending}
                    aria-invalid={errors.length > 0}
                    aria-describedby={
                      errors.length > 0 ? `${id}-description ${id}-error` : `${id}-description`
                    }
                    value={field.state.value}
                    onBlur={() => blurKeepingSubmitErrors(field)}
                    onChange={(event) => field.handleChange(event.target.value)}
                  />
                  <FieldDescription id={`${id}-description`}>{description}</FieldDescription>
                  <FieldError id={`${id}-error`} errors={errors} />
                </Field>
              );
            }}
          </form.Field>
        ))}
      </FieldGroup>

      <Button
        ref={submitRef}
        type="submit"
        className={`h-auto min-h-11 whitespace-normal ${focusClass}`}
        disabled={redeem.isPending}
      >
        {redeem.isPending ? "Changing password..." : "Change password"}
      </Button>

      {redeem.error && summary.length > 0 && (
        <div className="flex flex-col gap-2">
          <FieldError errors={summary} />
          {redeem.error.requestId && (
            <p className="text-sm text-muted-foreground">
              Request reference: <code className="break-all">{redeem.error.requestId}</code>
            </p>
          )}
        </div>
      )}
    </form>
  );
}
