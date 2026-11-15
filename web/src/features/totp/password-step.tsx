import { useEffect, useRef, type ReactElement } from "react";
import { revalidateLogic } from "@tanstack/react-form";
import type { PasswordProof } from "@/api/generated/model";
import { userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import { useAppForm } from "@/components/form/app-form";
import { useFormRequest } from "@/components/form/request";
import { RequestError } from "@/components/request-error";
import { FieldGroup } from "@/components/ui/field";
import { PasswordSchema } from "./schema";
import { checkingLabel, continueLabel, passwordLabel } from "./text";

const passwordFields = ["password"] as const;

// The part of a generated mutation hook's result the step sends with, e.g.
// useStartTOTPEnrollment()'s.
type PasswordMutation<TData> = {
  mutateAsync: (variables: { data: PasswordProof }) => Promise<TData>;
  error: ApiError | null;
  reset: () => void;
};

// PasswordStep asks for the account's password before something secret
// shows, e.g. the setup's QR code: a session cookie alone must not reveal
// it. lead says why, as the field's description, so a screen reader reads
// it with the field that takes focus. mutation sends the password; onDone
// receives its answer, and onRefused a failure, after a wrong password
// shows on the field. email fills a hidden username, so a password manager
// offers the password saved for this account.
export function PasswordStep<TData>({
  email,
  label,
  lead,
  mutation,
  onDone,
  onRefused,
}: {
  email: string;
  label: string;
  lead: string;
  mutation: PasswordMutation<TData>;
  onDone: (data: TData) => void;
  onRefused?: (err: ApiError) => void;
}): ReactElement {
  const fieldsRef = useRef<HTMLDivElement>(null);
  const request = useFormRequest(mutation, passwordFields);
  const form = useAppForm({
    defaultValues: { password: "" },
    validationLogic: revalidateLogic(),
    validators: { onDynamic: request.check(PasswordSchema) },
    onSubmit: async ({ value, formApi }) => {
      const sent = await request.send(formApi, value, { data: PasswordSchema.parse(value) });
      if (sent.outcome === "refused") onRefused?.(sent.error);
      if (sent.outcome !== "done") return;
      formApi.reset();
      onDone(sent.data);
    },
    listeners: { onChange: () => mutation.reset() },
  });

  // The password is what the step wants, so typing it works at once. This
  // runs after a signed-out page's title takes focus.
  useEffect(() => {
    fieldsRef.current?.querySelector("input")?.focus();
  }, []);

  return (
    <form.AppForm>
      <form.Form label={label} className="w-full max-w-sm">
        {/* Chrome and password managers match a password to its account by
            a username field in the same form (Chrome, "Create amazing
            password forms"). */}
        <input type="email" name="username" autoComplete="username" value={email} readOnly hidden />
        <FieldGroup ref={fieldsRef}>
          <form.AppField name="password">
            {(field) => (
              <field.PasswordField
                label={passwordLabel}
                description={lead}
                autoComplete="current-password"
              />
            )}
          </form.AppField>
        </FieldGroup>
        {/* A wrapper keeps the button its own width in the column. */}
        <div>
          <form.SubmitButton working={checkingLabel}>{continueLabel}</form.SubmitButton>
        </div>
        <RequestError errors={request.summary(userMessage)} requestId={request.reference} />
      </form.Form>
    </form.AppForm>
  );
}
