import { useId, type ChangeEvent, type ReactElement, type ReactNode } from "react";
import { useSelector } from "@tanstack/react-form";
import { CircleAlertIcon } from "lucide-react";
import { PasswordInput } from "@/components/password-input";
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { useFieldContext, useFormContext } from "./form-context";

// The words every shared field takes: its label, a description shown before
// the control, and whether the field may be left empty, which the label then
// says.
type FieldText = { label: string; description?: string; optional?: boolean };

// The props of the current field's control: they tie it to its label,
// description, and error, carry its value, and lock it while the form sends.
type ControlProps = {
  id: string;
  name: string;
  value: string;
  disabled: boolean;
  "aria-invalid": boolean;
  "aria-describedby": string | undefined;
  onBlur: () => void;
  onChange: (event: ChangeEvent<HTMLInputElement>) => void;
};

// EmailField is the current field as an email address input. autoComplete
// tells the browser which address it holds: "username" for one that signs
// in to inbok, so a password manager saves it with the password, "email" for
// another of the member's own, "off" for anyone else's.
export function EmailField({
  autoComplete,
  ...text
}: FieldText & { autoComplete: "username" | "email" | "off" }): ReactElement {
  return (
    <FieldFrame {...text}>
      {(control) => (
        <Input
          {...control}
          // The browser also strips spaces around the value of type "email"
          // (HTML's value sanitization), as the server does.
          type="email"
          autoComplete={autoComplete}
          autoCapitalize="none"
          spellCheck={false}
        />
      )}
    </FieldFrame>
  );
}

// PasswordField is the current field as a password input with a button that
// shows it. autoComplete is "current-password" to sign in, "new-password" to
// choose one, or "off" for a password that is not the member's own, e.g. a
// mail server's. toggle names the password in the button's label, e.g.
// "Show new password confirmation"; it defaults to the label in lowercase.
export function PasswordField({
  autoComplete,
  toggle,
  ...text
}: FieldText & {
  autoComplete: "current-password" | "new-password" | "off";
  toggle?: string;
}): ReactElement {
  return (
    <FieldFrame {...text}>
      {(control) => (
        <PasswordInput
          {...control}
          label={toggle ?? text.label.toLowerCase()}
          type="password"
          autoComplete={autoComplete}
          // Shown, the password is a text input, where a phone's keyboard
          // may capitalise or correct it (GOV.UK Design System, "Password
          // input": do not spellcheck or autocapitalise).
          autoCapitalize="none"
          spellCheck={false}
        />
      )}
    </FieldFrame>
  );
}

// FieldFrame renders the current text field's label, description, the
// control children returns from its props, and the field's errors under it.
// Every field kind in this file renders through it.
function FieldFrame({
  label,
  description,
  optional,
  children,
}: FieldText & { children: (control: ControlProps) => ReactNode }): ReactElement {
  const field = useFieldContext<string>();
  const form = useFormContext();
  const sending = useSelector(form.store, (state) => state.isSubmitting);
  const id = useId();
  const descriptionId = `${id}-description`;
  const errorId = `${id}-error`;
  const errors = messages(field.state.meta.errors);
  const invalid = errors.length > 0;
  const describedBy = [description && descriptionId, invalid && errorId].filter(Boolean).join(" ");

  return (
    <Field data-invalid={invalid} data-disabled={sending}>
      <FieldLabel htmlFor={id}>
        {label}
        {optional && <span className="font-normal text-muted-foreground">{" (optional)"}</span>}
      </FieldLabel>
      {description && <FieldDescription id={descriptionId}>{description}</FieldDescription>}
      {children({
        id,
        name: field.name,
        value: field.state.value,
        disabled: sending,
        "aria-invalid": invalid,
        "aria-describedby": describedBy || undefined,
        onBlur: field.handleBlur,
        onChange: (event) => field.handleChange(event.target.value),
      })}
      {invalid && (
        <div className="flex gap-1.5 text-destructive">
          <CircleAlertIcon aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
          <FieldError id={errorId} errors={errors} />
        </div>
      )}
    </Field>
  );
}

// messages returns a field's errors as messages: Zod's issues and the
// server's refusals carry one; a check returning text gives it whole.
function messages(errors: readonly unknown[]): { message: string }[] {
  return errors.flatMap((error) => {
    if (typeof error === "string") return [{ message: error }];
    if (typeof error === "object" && error !== null && "message" in error) {
      if (typeof error.message === "string") return [{ message: error.message }];
    }
    return [];
  });
}
