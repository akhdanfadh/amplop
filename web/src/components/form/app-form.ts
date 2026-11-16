import { createFormHook } from "@tanstack/react-form";
import {
  ChoiceField,
  CodeField,
  EmailField,
  FromField,
  MessageField,
  PasswordField,
  RecipientsField,
  SelectField,
  SubjectField,
  TextField,
} from "./fields";
import { Form, SubmitButton } from "./form";
import { fieldContext, formContext } from "./form-context";

// useAppForm is TanStack Form's useForm with the app's shared parts: each
// field renders as a field kind, e.g. field.TextField, field.EmailField,
// field.PasswordField, field.ChoiceField, field.SelectField, or
// field.CodeField, or a message
// editor's row, field.FromField, field.RecipientsField, field.SubjectField,
// or field.MessageField, inside
// form.AppField, and the form as form.Form with a
// form.SubmitButton inside form.AppForm. withFieldGroup builds a part
// several forms share, e.g. a message editor's recipient rows, over fields
// each names. web/README.md "Forms" shows a whole form.
export const { useAppForm, withFieldGroup } = createFormHook({
  fieldContext,
  formContext,
  fieldComponents: {
    ChoiceField,
    CodeField,
    EmailField,
    FromField,
    MessageField,
    PasswordField,
    RecipientsField,
    SelectField,
    SubjectField,
    TextField,
  },
  formComponents: { Form, SubmitButton },
});
