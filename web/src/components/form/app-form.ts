import { createFormHook } from "@tanstack/react-form";
import { ChoiceField, CodeField, EmailField, PasswordField, TextField } from "./fields";
import { Form, SubmitButton } from "./form";
import { fieldContext, formContext } from "./form-context";

// useAppForm is TanStack Form's useForm with the app's shared parts: each
// field renders as a field kind, e.g. field.TextField, field.EmailField,
// field.PasswordField, field.ChoiceField, or field.CodeField, inside
// form.AppField, and the form as form.Form with a
// form.SubmitButton inside form.AppForm. web/README.md "Forms" shows a
// whole form.
export const { useAppForm } = createFormHook({
  fieldContext,
  formContext,
  fieldComponents: { ChoiceField, CodeField, EmailField, PasswordField, TextField },
  formComponents: { Form, SubmitButton },
});
