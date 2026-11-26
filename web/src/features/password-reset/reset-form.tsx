import { useRef, type ReactElement } from "react";
import { revalidateLogic } from "@tanstack/react-form";
import { useQueryClient } from "@tanstack/react-query";
import { useRedeemPasswordResetLink } from "@/api/generated/amplop";
import {
  redeemPasswordResetLinkBodyPasswordMax,
  redeemPasswordResetLinkBodyPasswordMin,
} from "@/api/generated/model";
import { uncertainOutcome } from "@/api/problem";
import { useAppForm } from "@/components/form/app-form";
import { useFormRequest } from "@/components/form/request";
import { RequestError } from "@/components/request-error";
import { FieldGroup } from "@/components/ui/field";
import { clearAccountCache } from "@/features/auth/session-cache";
import { ResetSchema } from "./reset-schema";
import { completeErrorMessage, endOf, linkInvalid, type EarlierTries, type Ended } from "./text";

const resetFields = ["email", "password", "confirmPassword"] as const;

// ResetForm sets a new password from secret, the text after # in a reset
// link, the email the visitor types, which must be the one the link was sent
// to, and the password they choose. onDone receives the account's email,
// for signing in; onEnded receives the page's end once the link no longer
// works.
export function ResetForm({
  secret,
  onDone,
  onEnded,
}: {
  secret: string;
  onDone: (email: string) => void;
  onEnded: (ended: Ended) => void;
}): ReactElement {
  // A try whose outcome is unknown may have changed the password.
  const earlier = useRef<EarlierTries>("refused");
  const queryClient = useQueryClient();
  const redeem = useRedeemPasswordResetLink({
    mutation: {
      // Do not keep the chosen password after the form unmounts.
      gcTime: 0,
      // A reset signs the account out everywhere, this browser too, so its
      // cached data goes, even when the answer arrives after the form
      // unmounted. A result that may have reset counts as one.
      onSuccess: () => clearAccountCache(queryClient),
      onError: (err) => {
        if (uncertainOutcome(err)) clearAccountCache(queryClient);
      },
    },
  });
  const request = useFormRequest(redeem, resetFields);
  const form = useAppForm({
    defaultValues: { email: "", password: "", confirmPassword: "" },
    validationLogic: revalidateLogic(),
    validators: { onDynamic: request.check(ResetSchema) },
    onSubmit: async ({ value, formApi }) => {
      const sent = await request.send(formApi, value, {
        data: { secret, email: value.email, password: value.password },
      });
      if (sent.outcome === "done") {
        formApi.reset();
        onDone(sent.data.data.email);
        return;
      }
      if (sent.outcome !== "refused") return;

      // 1. A link that no longer works ends the page; the end needs neither
      // the password nor the secret.
      if (linkInvalid(sent.error)) {
        formApi.reset();
        redeem.reset();
        onEnded(endOf(sent.error, earlier.current));
        return;
      }
      // 2. Remember a try that may have reset, for the end's wording.
      if (uncertainOutcome(sent.error)) earlier.current = "unknown";
    },
    // An idle reset still re-renders the whole form, so only clear an error.
    listeners: {
      onChange: () => {
        if (redeem.error) redeem.reset();
      },
    },
  });

  return (
    <form.AppForm>
      <form.Form label="Choose a new password">
        <FieldGroup>
          <form.AppField name="email">
            {(field) => (
              <field.EmailField
                label="Email address"
                description="The address this link was sent to."
                autoComplete="username"
              />
            )}
          </form.AppField>
          <form.AppField name="password">
            {(field) => (
              <field.PasswordField
                label="New password"
                description={`Use ${redeemPasswordResetLinkBodyPasswordMin} to ${redeemPasswordResetLinkBodyPasswordMax} characters.`}
                autoComplete="new-password"
              />
            )}
          </form.AppField>
          <form.AppField name="confirmPassword">
            {(field) => (
              <field.PasswordField
                label="Confirm new password"
                description="Enter the same password again."
                autoComplete="new-password"
                toggle="new password confirmation"
              />
            )}
          </form.AppField>
        </FieldGroup>
        <form.SubmitButton working="Changing password...">Change password</form.SubmitButton>
        <RequestError
          errors={request.summary(completeErrorMessage)}
          requestId={request.reference}
        />
      </form.Form>
    </form.AppForm>
  );
}
