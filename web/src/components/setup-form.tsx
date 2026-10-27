import { useRef, useState, type ComponentProps, type ReactElement } from "react";
import { useForm, useSelector } from "@tanstack/react-form";
import { EyeIcon, EyeOffIcon } from "lucide-react";
import { useCreateFirstAdmin } from "@/api/generated/inbok";
import { newSetupPasswordMin, newSetupPasswordMax } from "@/api/generated/model";
import { formErrors, userMessage } from "@/api/problem";
import { SetupSchema } from "@/components/setup-schema";
import { Button } from "@/components/ui/button";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group";

const emptySetup = { token: "", email: "", password: "", confirmPassword: "" };
const conflict = 409;
const setupDoneType = "/api/v0/problems/setup-done";

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

export function SetupForm({ onDone }: { onDone: () => void }): ReactElement {
  const formRef = useRef<HTMLFormElement>(null);
  // Do not retain submitted credentials after the form is unmounted.
  const create = useCreateFirstAdmin({ mutation: { gcTime: 0 } });
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
            onDone();
          },
          onError: (err) => {
            if (err.status === conflict && err.problem?.type === setupDoneType) {
              formApi.reset();
              onDone();
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

function PasswordInput({
  label,
  ...props
}: ComponentProps<"input"> & { label: string }): ReactElement {
  const [visible, setVisible] = useState(false);
  const Icon = visible ? EyeOffIcon : EyeIcon;

  return (
    <InputGroup className="h-auto min-h-11 border-muted-foreground has-[[data-slot=input-group-control]:focus-visible]:outline-2 has-[[data-slot=input-group-control]:focus-visible]:outline-offset-2 has-[[data-slot=input-group-control]:focus-visible]:outline-foreground has-[[data-slot=input-group-control]:focus-visible]:outline-solid">
      <InputGroupInput {...props} className="h-11" type={visible ? "text" : "password"} />
      <InputGroupAddon align="inline-end" className="py-0">
        <InputGroupButton
          className="size-11 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
          size="icon-sm"
          aria-label={`${visible ? "Hide" : "Show"} ${label}`}
          aria-controls={props.id}
          disabled={props.disabled}
          onClick={() => setVisible((previous) => !previous)}
        >
          <Icon aria-hidden="true" data-icon="inline-end" />
        </InputGroupButton>
      </InputGroupAddon>
    </InputGroup>
  );
}
