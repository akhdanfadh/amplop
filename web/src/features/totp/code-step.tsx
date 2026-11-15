import { useEffect, useRef, type ReactElement, type ReactNode } from "react";
import { revalidateLogic } from "@tanstack/react-form";
import { userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import { useAppForm } from "@/components/form/app-form";
import { useFormRequest } from "@/components/form/request";
import { RequestError } from "@/components/request-error";
import { FieldGroup } from "@/components/ui/field";
import { codeFormats, codePaste } from "@/features/auth/code-schema";
import type { ReplacementCodeSchema } from "./schema";

const codeFields = ["code"] as const;
const authenticator = codeFormats.authenticator;

// The part of a generated mutation hook's result the form sends with, e.g.
// useReplaceTOTPBackupCodes()'s.
type CodeMutation<TData> = {
  mutateAsync: (variables: { data: { code: string } }) => Promise<TData>;
  error: ApiError | null;
  reset: () => void;
};

// CodeStep asks for a code from the authenticator app and sends
// it with mutation: onDone receives the answer, onRefused a failure, after
// its field error, if any, shows. label names the form for a screen reader;
// action and working are its button's words. The field takes focus when it
// appears: the member asked for the step, e.g. Replace backup codes.
// children follow the button, e.g. a way back.
export function CodeStep<TData>({
  label,
  action,
  working,
  schema,
  mutation,
  onDone,
  onRefused,
  children,
}: {
  label: string;
  action: string;
  working: string;
  schema: typeof ReplacementCodeSchema;
  mutation: CodeMutation<TData>;
  onDone: (data: TData) => void;
  onRefused?: (err: ApiError) => void;
  children?: ReactNode;
}): ReactElement {
  const fieldsRef = useRef<HTMLDivElement>(null);
  const request = useFormRequest(mutation, codeFields);
  const form = useAppForm({
    defaultValues: { code: "" },
    validationLogic: revalidateLogic(),
    validators: { onDynamic: request.check(schema) },
    onSubmit: async ({ value, formApi }) => {
      const sent = await request.send(formApi, value, { data: schema.parse(value) });
      if (sent.outcome === "done") {
        formApi.reset();
        onDone(sent.data);
      }
      if (sent.outcome === "refused") onRefused?.(sent.error);
    },
    listeners: { onChange: () => mutation.reset() },
  });

  useEffect(() => {
    fieldsRef.current?.querySelector("input")?.focus();
  }, []);

  return (
    <form.AppForm>
      <form.Form label={label} className="w-full max-w-sm">
        <FieldGroup ref={fieldsRef}>
          <form.AppField name="code">
            {(field) => (
              <field.CodeField
                label={authenticator.label}
                length={authenticator.length}
                pattern={authenticator.pattern}
                inputMode={authenticator.inputMode}
                paste={codePaste}
              />
            )}
          </form.AppField>
        </FieldGroup>
        <div className="flex flex-wrap gap-3">
          <form.SubmitButton working={working}>{action}</form.SubmitButton>
          {children}
        </div>
        <RequestError errors={request.summary(userMessage)} requestId={request.reference} />
      </form.Form>
    </form.AppForm>
  );
}
