import { useRef, type ReactElement } from "react";
import { revalidateLogic } from "@tanstack/react-form";
import { useRedeemRegistrationLink } from "@/api/generated/inbok";
import {
  redeemRegistrationLinkBodyPasswordMax,
  redeemRegistrationLinkBodyPasswordMin,
} from "@/api/generated/model";
import { uncertainOutcome } from "@/api/problem";
import { useAppForm } from "@/components/form/app-form";
import { useFormRequest } from "@/components/form/request";
import { RequestError } from "@/components/request-error";
import { FieldGroup } from "@/components/ui/field";
import { CompleteSchema } from "./complete-schema";
import {
  completeEnd,
  completeErrorMessage,
  endOf,
  type EarlierTries,
  type Ended,
} from "./visitor-text";

const completeFields = ["email", "password", "confirmPassword"] as const;

// CompleteForm creates a member account from secret, the text after # in a
// sign-up link, the email the visitor types, which must be the one the link
// was sent to, and the password they choose. onDone receives the account's
// email, for signing in; onEnded receives the page's end once no try with
// this link can work.
export function CompleteForm({
  secret,
  onDone,
  onEnded,
}: {
  secret: string;
  onDone: (email: string) => void;
  onEnded: (ended: Ended) => void;
}): ReactElement {
  // A try whose outcome is unknown may have created the account.
  const earlier = useRef<EarlierTries>("refused");
  // Do not keep the chosen password after the form unmounts.
  const redeem = useRedeemRegistrationLink({ mutation: { gcTime: 0 } });
  const request = useFormRequest(redeem, completeFields);
  const form = useAppForm({
    defaultValues: { email: "", password: "", confirmPassword: "" },
    validationLogic: revalidateLogic(),
    validators: { onDynamic: request.check(CompleteSchema) },
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

      // 1. A refusal no retry with this link can change ends the page; the
      // end needs neither the password nor the secret.
      const end = completeEnd(sent.error);
      if (end) {
        formApi.reset();
        redeem.reset();
        onEnded(endOf(sent.error, end, earlier.current));
        return;
      }
      // 2. Remember a try that may have created the account, for the end's
      // wording.
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
      <form.Form label="Create your account">
        <FieldGroup>
          <form.AppField name="email">
            {(field) => (
              <field.EmailField
                label="Email address"
                description="The address you asked to sign up with."
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
                description={`Use ${redeemRegistrationLinkBodyPasswordMin} to ${redeemRegistrationLinkBodyPasswordMax} characters.`}
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
        <form.SubmitButton working="Creating account...">Create account</form.SubmitButton>
        <RequestError
          errors={request.summary(completeErrorMessage)}
          requestId={request.reference}
        />
      </form.Form>
    </form.AppForm>
  );
}
