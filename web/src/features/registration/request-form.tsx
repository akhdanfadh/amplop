import { useEffect, useRef, type ReactElement } from "react";
import { revalidateLogic } from "@tanstack/react-form";
import { useQueryClient } from "@tanstack/react-query";
import { getGetRegistrationQueryKey, useRequestRegistrationLink } from "@/api/generated/inbok";
import { NewRegistrationRequest } from "@/api/generated/model";
import { emailAddress } from "@/api/validation";
import { useAppForm } from "@/components/form/app-form";
import { useFormRequest } from "@/components/form/request";
import { RequestError } from "@/components/request-error";
import { registrationClosed, requestErrorMessage } from "./visitor-text";

const requestFields = ["email"] as const;
const RequestSchema = NewRegistrationRequest.extend({
  email: emailAddress(NewRegistrationRequest.shape.email),
});

// RequestForm asks for a sign-up link to an email address, starting from
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
  const client = useQueryClient();
  const fieldsRef = useRef<HTMLDivElement>(null);
  const create = useRequestRegistrationLink({
    mutation: {
      gcTime: 0,
      // The page reads availability again and shows registration closed.
      onError: (err) => {
        if (registrationClosed(err))
          void client.invalidateQueries({ queryKey: getGetRegistrationQueryKey() });
      },
    },
  });
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
      <form.Form label="Ask for a sign-up link">
        <div ref={fieldsRef}>
          <form.AppField name="email">
            {(field) => <field.EmailField label="Email address" autoComplete="email" />}
          </form.AppField>
        </div>
        <form.SubmitButton working="Sending sign-up link...">Send sign-up link</form.SubmitButton>
        <RequestError errors={request.summary(requestErrorMessage)} requestId={request.reference} />
      </form.Form>
    </form.AppForm>
  );
}
