import { useRef, type ReactElement } from "react";
import { useForm, useSelector } from "@tanstack/react-form";
import { useCreateFirstAdmin } from "@/api/generated/inbok";
import { newSetupPasswordMin, newSetupPasswordMax, type ProblemType } from "@/api/generated/model";
import { formErrors, userMessage } from "@/api/problem";
import { SetupSchema } from "./schema";
import { Button } from "@/components/ui/button";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { PasswordInput } from "@/components/password-input";

const emptySetup = { token: "", email: "", password: "", confirmPassword: "" };
const conflict = 409;
const setupDoneType: ProblemType = "/api/v0/problems/setup-done";

const inputs = [
  {
    name: "token",
    label: "Setup token",
    type: "password",
    autoComplete: "off",
    description: "Enter the INBOK_SETUP_TOKEN configured on the server.",
  },
  {
    name: "email",
    label: "Email address",
    type: "email",
    autoComplete: "email",
    description: "The email address for the first administrator account.",
  },
  {
    name: "password",
    label: "Password",
    type: "password",
    autoComplete: "new-password",
    description: `Use ${newSetupPasswordMin} to ${newSetupPasswordMax} characters.`,
  },
  {
    name: "confirmPassword",
    label: "Confirm password",
    type: "password",
    autoComplete: "new-password",
    description: "Enter the same password again.",
  },
] as const;
const setupFields = inputs.map((input) => input.name);

export function SetupForm({
  onAttempt,
  onDone,
}: {
  onAttempt: () => void;
  onDone: () => void;
}): ReactElement {
  const formRef = useRef<HTMLFormElement>(null);
  // Do not retain submitted credentials after the form is unmounted.
  const create = useCreateFirstAdmin({
    mutation: {
      gcTime: 0,
      onMutate: onAttempt,
      onSuccess: onDone,
      onError: (err) => {
        if (err.status === conflict && err.problem?.type === setupDoneType) return onDone();
      },
    },
  });
  const form = useForm({
    defaultValues: emptySetup,
    validators: { onChange: SetupSchema },
    onSubmitInvalid: ({ formApi }) => {
      const name = setupFields.find((field) => formApi.getFieldMeta(field)?.errors.length);
      if (!name) return;

      formRef.current?.querySelector<HTMLInputElement>(`[name="${name}"]`)?.focus();
    },
    onSubmit: ({ value, formApi }) => {
      const { token, email, password } = value;
      create.mutate(
        { data: { token, email, password } },
        {
          onSuccess: () => {
            formApi.reset();
          },
          onError: (err) => {
            if (err.status === conflict && err.problem?.type === setupDoneType) {
              formApi.reset();
              return;
            }
            const { fields } = formErrors(err, setupFields);
            if (Object.keys(fields).length > 0) formApi.setErrorMap({ onSubmit: { fields } });
          },
        },
      );
    },
    listeners: { onChange: () => create.reset() },
  });
  const submitted = useSelector(form.store, (state) => state.submissionAttempts > 0);
  const inputErrors = create.error ? formErrors(create.error, setupFields) : undefined;

  return (
    <form
      ref={formRef}
      className="flex flex-col gap-6"
      noValidate
      aria-label="Create the first administrator"
      aria-busy={create.isPending}
      onSubmit={(event) => {
        event.preventDefault();
        void form.handleSubmit();
      }}
    >
      <FieldGroup>
        {inputs.map(({ name, label, type, autoComplete, description }) => (
          <form.Field key={name} name={name}>
            {(field) => {
              const errors = field.state.meta.isBlurred || submitted ? field.state.meta.errors : [];
              const id = `setup-${name}`;
              const inputProps = {
                id,
                name,
                type,
                autoComplete,
                placeholder: name === "email" ? "you@example.com" : undefined,
                autoCapitalize: "none",
                spellCheck: false,
                disabled: create.isPending,
                "aria-invalid": errors.length > 0,
                "aria-describedby":
                  errors.length > 0 ? `${id}-description ${id}-error` : `${id}-description`,
                value: field.state.value,
                onBlur: field.handleBlur,
                onChange: (event: React.ChangeEvent<HTMLInputElement>) =>
                  field.handleChange(event.target.value),
              };
              return (
                <Field data-invalid={errors.length > 0} data-disabled={create.isPending}>
                  <FieldLabel htmlFor={id}>{label}</FieldLabel>
                  {name === "password" || name === "confirmPassword" ? (
                    <PasswordInput
                      label={name === "password" ? "password" : "password confirmation"}
                      {...inputProps}
                    />
                  ) : (
                    <Input
                      className="h-11 border-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid dark:aria-invalid:border-destructive"
                      {...inputProps}
                    />
                  )}
                  <FieldDescription id={`${id}-description`}>{description}</FieldDescription>
                  <FieldError id={`${id}-error`} errors={errors} />
                </Field>
              );
            }}
          </form.Field>
        ))}
      </FieldGroup>

      <Button
        className="h-auto min-h-11 whitespace-normal focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
        type="submit"
        disabled={create.isPending}
      >
        {create.isPending ? "Creating administrator..." : "Create administrator"}
      </Button>

      {create.error && (
        <div className="flex flex-col gap-2">
          <FieldError
            errors={[{ message: userMessage(create.error) }, ...(inputErrors?.unmatched ?? [])]}
          />
          {create.error.requestId && (
            <p className="text-sm text-muted-foreground">
              Request reference: <code className="break-all">{create.error.requestId}</code>
            </p>
          )}
        </div>
      )}
    </form>
  );
}
