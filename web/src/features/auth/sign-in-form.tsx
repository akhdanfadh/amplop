import { useEffect, useRef, useState, type ReactElement } from "react";
import { revalidateLogic } from "@tanstack/react-form";
import { useQueryClient } from "@tanstack/react-query";
import { useCreateSession, useGetCurrentSession } from "@/api/generated/inbok";
import { formErrors, formSummary, uncertainOutcome, userMessage } from "@/api/problem";
import { ApiError } from "@/api/transport";
import { sessionMessage } from "./session-error";
import { clearAccountCache } from "./session-cache";
import { SignInSchema } from "./sign-in-schema";
import { useAppForm } from "@/components/form/app-form";
import { useRefusals } from "@/components/form/refusals";
import { RequestError } from "@/components/request-error";
import { Button } from "@/components/ui/button";
import { FieldGroup } from "@/components/ui/field";
import { useMounted } from "@/hooks/use-mounted";

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
  const mounted = useMounted();
  const refusals = useRefusals<{ email: string; password: string }>(signInFields);
  const form = useAppForm({
    defaultValues: { email, password: "" },
    validationLogic: revalidateLogic(),
    validators: { onDynamic: refusals.check(SignInSchema) },
    onSubmit: async ({ value, formApi }) => {
      if (checkRequest.current) return;
      try {
        const result = await create.mutateAsync({ data: SignInSchema.parse(value) });
        // A member who left while the request ran, e.g. for "Forgot
        // password?", stays where they went.
        if (!mounted()) return;
        refusals.record(formApi, value);
        switch (result.data.step) {
          case "complete":
            formApi.reset();
            onDone();
            break;
          case "totp":
            formApi.reset();
            onCodeRequired();
            break;
        }
      } catch (err) {
        if (!(err instanceof ApiError)) throw err;
        refusals.record(formApi, value, err);
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
  const summary = create.error
    ? formSummary(sessionMessage(create.error, "sign-in"), formErrors(create.error, signInFields))
    : [];

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
    const request = {};
    checkRequest.current = request;
    setChecking(true);
    setCheckMessage(undefined);
    const result = await current.refetch();
    if (checkRequest.current !== request) return;
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

        <RequestError errors={summary} requestId={create.error?.requestId} />
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
