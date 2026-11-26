import { useEffect, useRef, useState, type ReactElement } from "react";
import { revalidateLogic } from "@tanstack/react-form";
import { useQueryClient } from "@tanstack/react-query";
import { useCreateSessionWithCode, useGetCurrentSession } from "@/api/generated/amplop";
import { uncertainOutcome, userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import { cn } from "@/lib/cn";
import { useAppForm } from "@/components/form/app-form";
import { useFormRequest } from "@/components/form/request";
import { smallTarget } from "@/components/link-style";
import { RequestError } from "@/components/request-error";
import { Button } from "@/components/ui/button";
import { FieldGroup } from "@/components/ui/field";
import { clearAccountCache } from "./session-cache";
import { challengeEnded, sessionMessage } from "./session-error";
import { codeFormats, codePaste, codeSchema, type CodeKind } from "./code-schema";

const codeFields = ["code"] as const;
const unauthorized = 401;

// CodeForm finishes a sign-in with the code from the member's authenticator
// app, or a backup code. onDone reports the session made; onEnded reports
// a refusal that ends the sign-in attempt, after which only starting again
// can continue; onRestart goes back to the password step. The challenge
// stays in its HttpOnly cookie; this form only submits the proof.
export function CodeForm({
  onDone,
  onEnded,
  onRestart,
}: {
  onDone: () => void;
  onEnded: (err: ApiError) => void;
  onRestart: () => void;
}): ReactElement {
  const queryClient = useQueryClient();
  const fieldsRef = useRef<HTMLDivElement>(null);
  const checkRef = useRef<HTMLButtonElement>(null);
  const checkRequest = useRef<object | null>(null);
  const [checking, setChecking] = useState(false);
  const [checkMessage, setCheckMessage] = useState<string>();
  const [kind, setKind] = useState<CodeKind>("authenticator");
  const format = codeFormats[kind];
  const schema = codeSchema(kind);
  const current = useGetCurrentSession({
    query: { enabled: false, retry: false, gcTime: 0, networkMode: "always" },
  });
  const create = useCreateSessionWithCode({
    mutation: {
      gcTime: 0,
      // Cookie changes still require cleanup if the page closes mid-request.
      onSettled: () => clearAccountCache(queryClient),
    },
  });
  const request = useFormRequest(create, codeFields);
  const form = useAppForm({
    defaultValues: { code: "" },
    validationLogic: revalidateLogic(),
    validators: { onDynamic: request.check(schema) },
    onSubmit: async ({ value, formApi }) => {
      if (checkRequest.current) return;
      const sent = await request.send(formApi, value, { data: schema.parse(value) });
      if (sent.outcome === "done") {
        formApi.reset();
        onDone();
        return;
      }
      // No code can finish an ended attempt: the page ends, and the code
      // typed has no further use.
      if (sent.outcome === "refused" && challengeEnded(sent.error)) {
        formApi.reset();
        onEnded(sent.error);
      }
    },
    listeners: {
      onChange: () => {
        checkRequest.current = null;
        setChecking(false);
        void queryClient.cancelQueries({ queryKey: current.queryKey, exact: true });
        create.reset();
        setCheckMessage(undefined);
      },
    },
  });
  const summary = request.summary((err) => sessionMessage(err, "sign-in"));

  // The code goes in the field: on arrival, a step the member is in the
  // middle of, so typing the code at once works, and after a switch to the
  // other kind. This effect runs after the page's title takes focus.
  useEffect(() => {
    fieldsRef.current?.querySelector("input")?.focus();
    return () => {
      checkRequest.current = null;
    };
  }, [kind]);

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
        ? "You are not signed in. Try the code again, or start sign-in again."
        : result.error
          ? userMessage(result.error)
          : "Could not check your session. Try again.",
    );
  }

  function changeKind(): void {
    checkRequest.current = null;
    setChecking(false);
    setCheckMessage(undefined);
    void queryClient.cancelQueries({ queryKey: current.queryKey, exact: true });
    create.reset();
    form.reset();
    setKind(kind === "authenticator" ? "backup" : "authenticator");
  }

  return (
    <form.AppForm>
      <form.Form label="Verify sign-in">
        <FieldGroup ref={fieldsRef}>
          {/* A new kind is a new field, with nothing typed for the old one. */}
          <form.AppField key={kind} name="code">
            {(field) => (
              <field.CodeField
                label={format.label}
                description="Enter the current code from your authenticator app, or an unused backup code."
                length={format.length}
                pattern={format.pattern}
                inputMode={format.inputMode}
                paste={codePaste}
                labelAction={
                  <Button
                    type="button"
                    variant="link"
                    className={cn(smallTarget, "p-0 font-normal whitespace-normal")}
                    isDisabled={create.isPending}
                    onPress={changeKind}
                  >
                    {kind === "authenticator" ? "Use a backup code" : "Use authenticator app"}
                  </Button>
                }
              />
            )}
          </form.AppField>
        </FieldGroup>

        <form.SubmitButton working="Verifying..." isDisabled={checking}>
          Verify and sign in
        </form.SubmitButton>

        <RequestError errors={summary} requestId={request.reference} />
        {create.error && uncertainOutcome(create.error) && (
          <div className="flex flex-col">
            <Button
              ref={checkRef}
              type="button"
              variant="outline"
              className="whitespace-normal"
              isDisabled={checking}
              onPress={() => void checkSession()}
            >
              {checking ? "Checking your session..." : "Check session"}
            </Button>
            {/* Always present, so a screen reader announces the message that
                fills it; empty, it takes no space. */}
            <output className="not-empty:mt-3">{checkMessage}</output>
          </div>
        )}
        <Button
          type="button"
          variant="outline"
          className="whitespace-normal"
          isDisabled={create.isPending || checking}
          onPress={onRestart}
        >
          Start sign-in again
        </Button>
      </form.Form>
    </form.AppForm>
  );
}
