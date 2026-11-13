import { createFormHookContexts } from "@tanstack/react-form";

// The contexts the shared form components read: useFieldContext gives the
// field a component renders, useFormContext the form around it.
export const { fieldContext, formContext, useFieldContext, useFormContext } =
  createFormHookContexts();
