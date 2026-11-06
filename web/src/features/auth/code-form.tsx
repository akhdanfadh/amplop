import { Fragment, useEffect, useRef, useState, type ReactElement } from "react";
import { useForm, useSelector } from "@tanstack/react-form";
import { useQueryClient } from "@tanstack/react-query";
import { useCreateSessionWithCode, useGetCurrentSession } from "@/api/generated/inbok";
import { formErrors, formSummary, uncertainOutcome, userMessage } from "@/api/problem";
import { RequestError } from "@/components/request-error";
import { Button } from "@/components/ui/button";
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import {
  InputOTP,
  InputOTPGroup,
  InputOTPSeparator,
  InputOTPSlot,
} from "@/components/ui/input-otp";
import { blurKeepingSubmitErrors } from "@/lib/blur-keeping-submit-errors";
import { clearAccountCache } from "./session-cache";
import { challengeEnded, sessionMessage } from "./session-error";
import { codeFormats, codePaste, codeSchema, type CodeKind } from "./code-schema";

const codeFields = ["code"] as const;
const unauthorized = 401;

// The challenge stays in its HttpOnly cookie; this form only submits the proof.
export function CodeForm({
  onDone,
  onRestart,
}: {
  onDone: () => void;
  onRestart: () => void;
}): ReactElement {
  const queryClient = useQueryClient();
  const inputRef = useRef<HTMLInputElement>(null);
  const submitRef = useRef<HTMLButtonElement>(null);
  const restartRef = useRef<HTMLButtonElement>(null);
  const checkRef = useRef<HTMLButtonElement>(null);
  const checkRequest = useRef<object | null>(null);
  const [checking, setChecking] = useState(false);
  const [checkMessage, setCheckMessage] = useState<string>();
  const [kind, setKind] = useState<CodeKind>("authenticator");
  const format = codeFormats[kind];
  const schema = codeSchema(kind);
  const groupSize = format.length / 2;
  const groups = [0, groupSize];
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
  const ended = create.error ? challengeEnded(create.error) : false;
  const form = useForm({
    defaultValues: { code: "" },
    validators: { onChange: schema },
    onSubmitInvalid: () => inputRef.current?.focus(),
    onSubmit: ({ value, formApi }) => {
      if (create.isPending || checkRequest.current || ended) return;

      create.mutate(
        { data: schema.parse(value) },
        {
          onSuccess: () => {
            formApi.reset();
            onDone();
          },
          onError: (err) => {
            const { fields } = formErrors(err, codeFields);
            if (Object.keys(fields).length) formApi.setErrorMap({ onSubmit: { fields } });
          },
        },
      );
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
  const submitted = useSelector(form.store, (state) => state.submissionAttempts > 0);
  const inputErrors = create.error ? formErrors(create.error, codeFields) : undefined;
  const summary =
    create.error && inputErrors
      ? formSummary(sessionMessage(create.error, "sign-in"), inputErrors)
      : [];
  const codeRejected = Boolean(inputErrors?.fields.code);

  useEffect(() => {
    inputRef.current?.focus();
    return () => {
      checkRequest.current = null;
    };
  }, [kind]);

  useEffect(() => {
    if (!create.error || create.isPending) return;
    if (ended) restartRef.current?.focus();
    else (codeRejected ? inputRef.current : submitRef.current)?.focus();
  }, [create.error, create.isPending, ended, codeRejected]);

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
    <form
      className="flex flex-col gap-6"
      noValidate
      aria-label="Verify sign-in"
      aria-busy={create.isPending || checking}
      onSubmit={(event) => {
        event.preventDefault();
        void form.handleSubmit();
      }}
    >
      <FieldGroup>
        <form.Field name="code">
          {(field) => {
            const errors = field.state.meta.isBlurred || submitted ? field.state.meta.errors : [];
            return (
              <Field data-invalid={errors.length > 0} data-disabled={create.isPending || ended}>
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <FieldLabel htmlFor="sign-in-code">{format.label}</FieldLabel>
                  <Button
                    type="button"
                    variant="link"
                    className="min-h-11 w-fit whitespace-normal focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
                    disabled={create.isPending || ended}
                    onClick={changeKind}
                  >
                    {kind === "authenticator" ? "Use a backup code" : "Use authenticator app"}
                  </Button>
                </div>
                <InputOTP
                  key={kind}
                  ref={inputRef}
                  id="sign-in-code"
                  name="code"
                  maxLength={format.length}
                  pattern={format.pattern}
                  inputMode={kind === "authenticator" ? "numeric" : "text"}
                  pasteTransformer={codePaste}
                  autoComplete="one-time-code"
                  autoCapitalize="none"
                  spellCheck={false}
                  containerClassName="max-w-full flex-wrap gap-2"
                  disabled={create.isPending || ended}
                  aria-invalid={errors.length > 0}
                  aria-describedby={errors.length ? "sign-in-code-error" : undefined}
                  value={field.state.value}
                  onBlur={() => blurKeepingSubmitErrors(field)}
                  onChange={field.handleChange}
                >
                  {groups.map((offset) => (
                    <Fragment key={offset}>
                      <InputOTPGroup className="min-w-0 flex-1 basis-24">
                        {Array.from({ length: groupSize }, (_, index) => (
                          <InputOTPSlot
                            key={offset + index}
                            index={offset + index}
                            aria-invalid={errors.length > 0}
                          />
                        ))}
                      </InputOTPGroup>
                      {offset === 0 && <InputOTPSeparator />}
                    </Fragment>
                  ))}
                </InputOTP>
                <FieldError id="sign-in-code-error" errors={errors} />
              </Field>
            );
          }}
        </form.Field>
      </FieldGroup>

      <Button
        ref={submitRef}
        className="h-auto min-h-11 whitespace-normal focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
        type="submit"
        disabled={create.isPending || checking || ended}
      >
        {create.isPending ? "Verifying..." : "Verify and sign in"}
      </Button>

      <RequestError errors={summary} requestId={create.error?.requestId} />
      {create.error && uncertainOutcome(create.error) && (
        <div className="flex flex-col gap-3">
          <Button
            ref={checkRef}
            type="button"
            variant="outline"
            className="h-auto min-h-11 border-muted-foreground whitespace-normal focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
            disabled={checking}
            onClick={() => void checkSession()}
          >
            {checking ? "Checking your session..." : "Check session"}
          </Button>
          <output>{checkMessage}</output>
        </div>
      )}
      <Button
        ref={restartRef}
        type="button"
        variant="outline"
        className="h-auto min-h-11 border-muted-foreground whitespace-normal focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
        disabled={create.isPending || checking}
        onClick={onRestart}
      >
        Start sign-in again
      </Button>
    </form>
  );
}
