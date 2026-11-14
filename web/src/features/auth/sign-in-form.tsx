import { useEffect, useRef, useState, type ReactElement } from "react";
import { revalidateLogic } from "@tanstack/react-form";
import { useQueryClient } from "@tanstack/react-query";
import { useCreateSession, useGetCurrentSession } from "@/api/generated/inbok";
import { uncertainOutcome, userMessage } from "@/api/problem";
import { sessionMessage } from "./session-error";
import { clearAccountCache } from "./session-cache";
import { SignInSchema } from "./sign-in-schema";
import { useAppForm } from "@/components/form/app-form";
import { useFormRequest } from "@/components/form/request";
import { RequestError } from "@/components/request-error";
import { Button } from "@/components/ui/button";
import { FieldGroup } from "@/components/ui/field";

const unauthorized = 401;
const signInFields = ["email", "password"] as const;

// SignInForm submits credentials and reports the completed sign-in step.
// email fills in the email field, e.g. for an account just created.
export function SignInForm({
  email = "",
  onDone,
  onCodeRequired,
}: {
  email?: string;
  onDone: () => void;
  onCodeRequired: () => void;
}): ReactElement {
  const queryClient = useQueryClient();
  const checkRef = useRef<HTMLButtonElement>(null);
  const [checkMessage, setCheckMessage] = useState<string>();
  const [checking, setChecking] = useState(false);
  const checkRequest = useRef<object | null>(null);
  const current = useGetCurrentSession({
    query: { enabled: false, retry: false, gcTime: 0, networkMode: "always" },
  });
  // Release credentials from the mutation cache when the form unmounts.
  const create = useCreateSession({
    mutation: {
      gcTime: 0,
      // The response may change the cookie even after this form unmounts.
      onSettled: () => clearAccountCache(queryClient),
    },
  });
  const request = useFormRequest(create, signInFields);
  const form = useAppForm({
    defaultValues: { email, password: "" },
    validationLogic: revalidateLogic(),
    validators: { onDynamic: request.check(SignInSchema) },
    onSubmit: async ({ value, formApi }) => {
      if (checkRequest.current) return;
      const sent = await request.send(formApi, value, { data: SignInSchema.parse(value) });
      if (sent.outcome !== "done") return;
      formApi.reset();
      switch (sent.data.data.step) {
        case "complete":
          onDone();
          break;
        case "totp":
          onCodeRequired();
          break;
      }
    },
    listeners: {
      onChange: () => {
        if (checkRequest.current) {
          checkRequest.current = null;
          setChecking(false);
          void queryClient.cancelQueries({ queryKey: current.queryKey, exact: true });
        }
        create.reset();
        setCheckMessage(undefined);
      },
    },
  });
  const summary = request.summary((err) => sessionMessage(err, "sign-in"));

  useEffect(
    () => () => {
      // A completed check must not navigate after the user leaves this form.
      checkRequest.current = null;
    },
    [],
  );

  useEffect(() => {
    if (checkMessage && !checking) checkRef.current?.focus();
  }, [checkMessage, checking]);

  async function checkSession(): Promise<void> {
    if (create.isPending || checkRequest.current) return;
    const attempt = {};
    checkRequest.current = attempt;
    setChecking(true);
    setCheckMessage(undefined);
    const result = await current.refetch();
    if (checkRequest.current !== attempt) return;
    checkRequest.current = null;
    setChecking(false);
    if (result.isSuccess) {
      onDone();
      return;
    }
    setCheckMessage(
      result.error?.status === unauthorized
        ? "You are not signed in. Try signing in again."
        : result.error
          ? userMessage(result.error)
          : "Could not check your session. Try again.",
    );
  }

  return (
    <form.AppForm>
      <form.Form label="Sign in">
        <FieldGroup>
          <form.AppField name="email">
            {(field) => <field.EmailField label="Email address" autoComplete="username" />}
          </form.AppField>
          <form.AppField name="password">
            {(field) => <field.PasswordField label="Password" autoComplete="current-password" />}
          </form.AppField>
        </FieldGroup>

        <form.SubmitButton working="Signing in..." isDisabled={checking}>
          Sign in
        </form.SubmitButton>

        <RequestError errors={summary} requestId={request.reference} />
        {create.error && uncertainOutcome(create.error) && (
          <div className="flex flex-col gap-3">
            <Button
              ref={checkRef}
              type="button"
              variant="outline"
              isDisabled={checking}
              onPress={() => void checkSession()}
            >
              {checking ? "Checking your session..." : "Check session"}
            </Button>
            <output>{checkMessage}</output>
          </div>
        )}
      </form.Form>
    </form.AppForm>
  );
}
