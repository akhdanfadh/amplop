import { useEffect, useRef, type ReactElement } from "react";
import { revalidateLogic } from "@tanstack/react-form";
import { useRequestPasswordResetLink } from "@/api/generated/inbok";
import { NewPasswordResetRequest } from "@/api/generated/model";
import { emailAddress } from "@/api/validation";
import { useAppForm } from "@/components/form/app-form";
import { useFormRequest } from "@/components/form/request";
import { RequestError } from "@/components/request-error";
import { requestErrorMessage } from "./text";

const requestFields = ["email"] as const;
const RequestSchema = NewPasswordResetRequest.extend({
  email: emailAddress(NewPasswordResetRequest.shape.email),
});

// RequestForm asks for a reset link to an email address, starting from
// email, and reports the address onSent once the request is accepted.
// shown is "again" for a form shown again to fix a typo, which puts focus
// in the field; "first" leaves focus to the page.
export function RequestForm({
  email,
  shown,
  onSent,
}: {
  email: string;
  shown: "first" | "again";
  onSent: (email: string) => void;
}): ReactElement {
  const fieldsRef = useRef<HTMLDivElement>(null);
  const create = useRequestPasswordResetLink({ mutation: { gcTime: 0 } });
  const request = useFormRequest(create, requestFields);
  const form = useAppForm({
    defaultValues: { email },
    validationLogic: revalidateLogic(),
    validators: { onDynamic: request.check(RequestSchema) },
    onSubmit: async ({ value, formApi }) => {
      const sent = await request.send(formApi, value, { data: RequestSchema.parse(value) });
      if (sent.outcome === "done") onSent(value.email);
    },
    // An idle reset still re-renders the whole form, so only clear an error.
    listeners: {
      onChange: () => {
        if (create.error) create.reset();
      },
    },
  });

  // Shown again, the last address stays, so a typo takes one edit.
  useEffect(() => {
    if (shown === "again") fieldsRef.current?.querySelector("input")?.focus();
  }, [shown]);

  return (
    <form.AppForm>
      <form.Form label="Ask for a reset link">
        <div ref={fieldsRef}>
          <form.AppField name="email">
            {(field) => <field.EmailField label="Email address" autoComplete="username" />}
          </form.AppField>
        </div>
        <form.SubmitButton working="Sending reset link...">Send reset link</form.SubmitButton>
        <RequestError errors={request.summary(requestErrorMessage)} requestId={request.reference} />
      </form.Form>
    </form.AppForm>
  );
}
