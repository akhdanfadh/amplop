import { useEffect, useRef, useState, type ComponentProps, type ReactElement } from "react";
import { useForm, useSelector } from "@tanstack/react-form";
import { useQueryClient } from "@tanstack/react-query";
import { useCreateSession, useGetCurrentSession } from "@/api/generated/inbok";
import { formErrors, formSummary, uncertainOutcome, userMessage } from "@/api/problem";
import { sessionMessage } from "./session-error";
import { clearAccountCache } from "./session-cache";
import { SignInSchema } from "./sign-in-schema";
import { RequestError } from "@/components/request-error";
import { Button } from "@/components/ui/button";
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { useFocusAfterError } from "@/hooks/use-focus-after-error";
import { PasswordInput } from "@/components/password-input";
import { blurKeepingSubmitErrors } from "@/lib/blur-keeping-submit-errors";

const unauthorized = 401;
const inputs = [
  { name: "email", label: "Email address", type: "email", autoComplete: "username" },
  { name: "password", label: "Password", type: "password", autoComplete: "current-password" },
] as const;
const signInFields = inputs.map((input) => input.name);

// SignInForm submits credentials and reports the completed sign-in step.
// email fills in the email field, e.g. for an account just created.
export function SignInForm({
  email = "",
  onDone,
  onCodeRequired,
}: {
  email?: string;
  onDone: () => void;
  onCodeRequired: () => void;
}): ReactElement {
  const queryClient = useQueryClient();
  const formRef = useRef<HTMLFormElement>(null);
  const submitRef = useRef<HTMLButtonElement>(null);
  const checkRef = useRef<HTMLButtonElement>(null);
  const [checkMessage, setCheckMessage] = useState<string>();
  const [checking, setChecking] = useState(false);
  const checkRequest = useRef<object | null>(null);
  const current = useGetCurrentSession({
    query: { enabled: false, retry: false, gcTime: 0, networkMode: "always" },
  });
  // Release credentials from the mutation cache when the form unmounts.
  const create = useCreateSession({
    mutation: {
      gcTime: 0,
      // The response may change the cookie even after this form unmounts.
      onSettled: () => clearAccountCache(queryClient),
    },
  });
  const form = useForm({
    defaultValues: { email, password: "" },
    validators: { onChange: SignInSchema },
    onSubmitInvalid: ({ formApi }) => {
      const name = signInFields.find((field) => formApi.getFieldMeta(field)?.errors.length);
      if (name) formRef.current?.querySelector<HTMLInputElement>(`[name="${name}"]`)?.focus();
    },
    onSubmit: ({ value, formApi }) => {
      if (create.isPending || checkRequest.current) return;
      create.mutate(
        { data: SignInSchema.parse(value) },
        {
          onSuccess: (result) => {
            switch (result.data.step) {
              case "complete":
                formApi.reset();
                onDone();
                break;
              case "totp":
                formApi.reset();
                onCodeRequired();
                break;
            }
          },
          onError: (err) => {
            const { fields } = formErrors(err, signInFields);
            if (Object.keys(fields).length > 0) formApi.setErrorMap({ onSubmit: { fields } });
          },
        },
      );
    },
    listeners: {
      onChange: () => {
        if (checkRequest.current) {
          checkRequest.current = null;
          setChecking(false);
          void queryClient.cancelQueries({ queryKey: current.queryKey, exact: true });
        }
        create.reset();
        setCheckMessage(undefined);
      },
    },
  });
  const submitted = useSelector(form.store, (state) => state.submissionAttempts > 0);
  const summary = create.error
    ? formSummary(sessionMessage(create.error, "sign-in"), formErrors(create.error, signInFields))
    : [];

  useEffect(
    () => () => {
      // A completed check must not navigate after the user leaves this form.
      checkRequest.current = null;
    },
    [],
  );

  useFocusAfterError(create.error, create.isPending, formRef, submitRef);

  useEffect(() => {
    if (checkMessage && !checking) checkRef.current?.focus();
  }, [checkMessage, checking]);

  async function checkSession(): Promise<void> {
    if (create.isPending || checkRequest.current) return;
    const request = {};
    checkRequest.current = request;
    setChecking(true);
    setCheckMessage(undefined);
    const result = await current.refetch();
    if (checkRequest.current !== request) return;
    checkRequest.current = null;
    setChecking(false);
    if (result.isSuccess) {
      onDone();
      return;
    }
    setCheckMessage(
      result.error?.status === unauthorized
        ? "You are not signed in. Try signing in again."
        : result.error
          ? userMessage(result.error)
          : "Could not check your session. Try again.",
    );
  }

  return (
    <form
      ref={formRef}
      className="flex flex-col gap-6"
      noValidate
      aria-label="Sign in"
      aria-busy={create.isPending}
      onSubmit={(event) => {
        event.preventDefault();
        void form.handleSubmit();
      }}
    >
      <FieldGroup>
        {inputs.map(({ name, label, type, autoComplete }) => (
          <form.Field key={name} name={name}>
            {(field) => {
              const errors = field.state.meta.isBlurred || submitted ? field.state.meta.errors : [];
              const id = `sign-in-${name}`;
              const inputProps: ComponentProps<"input"> = {
                id,
                name,
                type,
                autoComplete,
                autoCapitalize: "none",
                spellCheck: false,
                disabled: create.isPending,
                "aria-invalid": errors.length > 0,
                "aria-describedby": errors.length > 0 ? `${id}-error` : undefined,
                value: field.state.value,
                onBlur: () => blurKeepingSubmitErrors(field),
                onChange: (event) => field.handleChange(event.target.value),
              };
              return (
                <Field data-invalid={errors.length > 0} data-disabled={create.isPending}>
                  <FieldLabel htmlFor={id}>{label}</FieldLabel>
                  {name === "password" ? (
                    <PasswordInput label="password" {...inputProps} />
                  ) : (
                    <Input
                      className="h-11 border-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid dark:aria-invalid:border-destructive"
                      {...inputProps}
                    />
                  )}
                  <FieldError id={`${id}-error`} errors={errors} />
                </Field>
              );
            }}
          </form.Field>
        ))}
      </FieldGroup>

      <Button
        ref={submitRef}
        className="h-auto min-h-11 whitespace-normal focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
        type="submit"
        disabled={create.isPending || checking}
      >
        {create.isPending ? "Signing in..." : "Sign in"}
      </Button>

      <RequestError errors={summary} requestId={create.error?.requestId} />
      {create.error && uncertainOutcome(create.error) && (
        <div className="flex flex-col gap-3">
          <Button
            ref={checkRef}
            type="button"
            variant="outline"
            className="min-h-11 border-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
            disabled={checking}
            onClick={() => void checkSession()}
          >
            {checking ? "Checking your session..." : "Check session"}
          </Button>
          <output>{checkMessage}</output>
        </div>
      )}
    </form>
  );
}
