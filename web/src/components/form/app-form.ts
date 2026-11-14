import { createFormHook } from "@tanstack/react-form";
import { CodeField, EmailField, PasswordField } from "./fields";
import { Form, SubmitButton } from "./form";
import { fieldContext, formContext } from "./form-context";

// useAppForm is TanStack Form's useForm with the app's shared parts: each
// field renders as field.EmailField, field.PasswordField, or
// field.CodeField inside form.AppField, and the form as form.Form with a
// form.SubmitButton inside form.AppForm. web/README.md "Forms" shows a
// whole form.
export const { useAppForm } = createFormHook({
  fieldContext,
  formContext,
  fieldComponents: { CodeField, EmailField, PasswordField },
  formComponents: { Form, SubmitButton },
});
