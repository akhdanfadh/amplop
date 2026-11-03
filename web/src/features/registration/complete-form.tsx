import { useEffect, useRef, useState, type ReactElement } from "react";
import { useForm, useSelector } from "@tanstack/react-form";
import { Link } from "@tanstack/react-router";
import { useRedeemRegistrationLink } from "@/api/generated/inbok";
import {
  redeemRegistrationLinkBodyPasswordMax,
  redeemRegistrationLinkBodyPasswordMin,
} from "@/api/generated/model";
import { formErrors, uncertainOutcome } from "@/api/problem";
import { PasswordInput } from "@/components/password-input";
import { Button } from "@/components/ui/button";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { CompleteSchema } from "./complete-schema";
import {
  completeEnd,
  completeErrorMessage,
  endOf,
  type EarlierTries,
  type Ended,
} from "./visitor-text";

const emptyValues = { email: "", password: "", confirmPassword: "" };
const emailId = "register-email";
const focusClass =
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid";
const linkClass = `inline-flex min-h-11 w-fit items-center underline underline-offset-4 ${focusClass}`;

const inputs = [
  {
    name: "password",
    label: "Password",
    description: `Use ${redeemRegistrationLinkBodyPasswordMin} to ${redeemRegistrationLinkBodyPasswordMax} characters.`,
    toggle: "password",
  },
  {
    name: "confirmPassword",
    label: "Confirm password",
    description: "Enter the same password again.",
    toggle: "password confirmation",
  },
] as const;
const completeFields = ["email", ...inputs.map((input) => input.name)] as const;

// CompleteForm creates a member account from secret, the text after # in a
// sign-up link, the email the visitor types, which must be the one the link
// was sent to, and the password they choose. onDone receives the account's
// email, for signing in.
export function CompleteForm({
  secret,
  onDone,
}: {
  secret: string;
  onDone: (email: string) => void;
}): ReactElement {
  const formRef = useRef<HTMLFormElement>(null);
  const submitRef = useRef<HTMLButtonElement>(null);
  const endRef = useRef<HTMLDivElement>(null);
  // A try whose outcome is unknown may have created the account.
  const earlier = useRef<EarlierTries>("refused");
  const [ended, setEnded] = useState<Ended>();
  // Do not keep the chosen password after the form unmounts.
  const redeem = useRedeemRegistrationLink({ mutation: { gcTime: 0 } });
  const form = useForm({
    defaultValues: emptyValues,
    validators: { onChange: CompleteSchema },
    onSubmitInvalid: ({ formApi }) => {
      const name = completeFields.find((field) => formApi.getFieldMeta(field)?.errors.length);
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
            const end = completeEnd(err);
            if (end) {
              setEnded(endOf(err, end, earlier.current));
              // The end screen needs neither the password nor the secret.
              formApi.reset();
              redeem.reset();
              return;
            }

            if (uncertainOutcome(err)) earlier.current = "unknown";
            const { fields } = formErrors(err, completeFields);
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
  const inputErrors = redeem.error ? formErrors(redeem.error, completeFields) : undefined;

  // The disabled button lost focus while saving; the field a refusal names,
  // or the button, takes it.
  useEffect(() => {
    if (!redeem.error || redeem.isPending) return;
    const invalid = formRef.current?.querySelector<HTMLInputElement>('[aria-invalid="true"]');
    (invalid ?? submitRef.current)?.focus();
  }, [redeem.error, redeem.isPending]);

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
            <Link key={link} to="/register" className={linkClass}>
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
      aria-label="Create your account"
      aria-busy={redeem.isPending}
      onSubmit={(event) => {
        event.preventDefault();
        void form.handleSubmit();
      }}
    >
      <p>
        Enter the email address this link was sent to and choose a password for your new inbok
        account. If you didn&apos;t ask to create an account, close this page.
      </p>
      <FieldGroup>
        <form.Field name="email">
          {(field) => {
            const errors = field.state.meta.isBlurred || submitted ? field.state.meta.errors : [];
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
                  aria-describedby={
                    errors.length > 0
                      ? `${emailId}-description ${emailId}-error`
                      : `${emailId}-description`
                  }
                  value={field.state.value}
                  onBlur={field.handleBlur}
                  onChange={(event) => field.handleChange(event.target.value)}
                />
                <FieldDescription id={`${emailId}-description`}>
                  The address you asked to sign up with.
                </FieldDescription>
                <FieldError id={`${emailId}-error`} errors={errors} />
              </Field>
            );
          }}
        </form.Field>
        {inputs.map(({ name, label, description, toggle }) => (
          <form.Field key={name} name={name}>
            {(field) => {
              const errors = field.state.meta.isBlurred || submitted ? field.state.meta.errors : [];
              const id = `register-${name}`;
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
                    onBlur={field.handleBlur}
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
        {redeem.isPending ? "Creating account..." : "Create account"}
      </Button>

      {redeem.error && (
        <div className="flex flex-col gap-2">
          <FieldError
            errors={[
              { message: completeErrorMessage(redeem.error) },
              ...(inputErrors?.unmatched ?? []),
            ]}
          />
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
