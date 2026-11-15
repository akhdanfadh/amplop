import type { ReactElement } from "react";
import { revalidateLogic } from "@tanstack/react-form";
import { useCreateFirstAdmin } from "@/api/generated/inbok";
import { newSetupPasswordMax, newSetupPasswordMin, type ProblemType } from "@/api/generated/model";
import { userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import { useAppForm } from "@/components/form/app-form";
import { useFormRequest } from "@/components/form/request";
import { RequestError } from "@/components/request-error";
import { FieldGroup } from "@/components/ui/field";
import { SetupSchema } from "./schema";

const conflict = 409;
const setupDoneType: ProblemType = "/api/v0/problems/setup-done";
const setupFields = ["token", "email", "password", "confirmPassword"] as const;

// setupDone reports whether err refused setup because it is already done.
function setupDone(err: ApiError): boolean {
  return err.status === conflict && err.problem?.type === setupDoneType;
}

// SetupForm creates the first administrator with the server's setup token.
// onAttempt reports that a try started, which may complete setup even if
// its answer is lost; onDone, that setup is complete, here or before.
export function SetupForm({
  onAttempt,
  onDone,
}: {
  onAttempt: () => void;
  onDone: () => Promise<void>;
}): ReactElement {
  const create = useCreateFirstAdmin({
    mutation: {
      // Do not retain submitted credentials after the form is unmounted.
      gcTime: 0,
      // On the mutation, not the form, so they run even if the page has
      // gone when the answer comes.
      onMutate: onAttempt,
      onSuccess: onDone,
      onError: async (err) => {
        if (setupDone(err)) await onDone();
      },
    },
  });
  const request = useFormRequest(create, setupFields);
  const form = useAppForm({
    defaultValues: { token: "", email: "", password: "", confirmPassword: "" },
    validationLogic: revalidateLogic(),
    validators: { onDynamic: request.check(SetupSchema) },
    onSubmit: async ({ value, formApi }) => {
      const { token, email, password } = value;
      const sent = await request.send(formApi, value, { data: { token, email, password } });
      // Done here or before, the credentials have no further use.
      if (sent.outcome === "done" || (sent.outcome === "refused" && setupDone(sent.error)))
        formApi.reset();
    },
    listeners: { onChange: () => create.reset() },
  });

  return (
    <form.AppForm>
      <form.Form label="Create the first administrator">
        <FieldGroup>
          <form.AppField name="token">
            {(field) => (
              <field.PasswordField
                label="Setup token"
                description="Enter the INBOK_SETUP_TOKEN configured on the server."
                autoComplete="off"
              />
            )}
          </form.AppField>
          <form.AppField name="email">
            {(field) => (
              <field.EmailField
                label="Email address"
                description="The email address for the first administrator account."
                // "username" lets a password manager save it with the new
                // password as one login.
                autoComplete="username"
              />
            )}
          </form.AppField>
          <form.AppField name="password">
            {(field) => (
              <field.PasswordField
                label="Password"
                description={`Use ${newSetupPasswordMin} to ${newSetupPasswordMax} characters.`}
                autoComplete="new-password"
              />
            )}
          </form.AppField>
          <form.AppField name="confirmPassword">
            {(field) => (
              <field.PasswordField
                label="Confirm password"
                description="Enter the same password again."
                autoComplete="new-password"
                toggle="password confirmation"
              />
            )}
          </form.AppField>
        </FieldGroup>
        <form.SubmitButton working="Creating administrator...">
          Create administrator
        </form.SubmitButton>
        <RequestError errors={request.summary(userMessage)} requestId={request.reference} />
      </form.Form>
    </form.AppForm>
  );
}
