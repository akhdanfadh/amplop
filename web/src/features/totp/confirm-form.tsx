import { Fragment, useEffect, useRef, useState } from "react";
import { useForm, useSelector } from "@tanstack/react-form";
import type { ApiError } from "@/api/transport";
import { formErrors, userMessage } from "@/api/problem";
import { codeFormats, codePaste, type CodeKind } from "@/features/auth/code-schema";
import { uncertainSession } from "@/features/auth/session-error";
import { Button } from "@/components/ui/button";
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import {
  InputOTP,
  InputOTPGroup,
  InputOTPSeparator,
  InputOTPSlot,
} from "@/components/ui/input-otp";
import { ConfirmationSchema, RegenerationSchema, turnOffSchema } from "./schema";

const fieldNames = ["code"] as const;
const purposes = {
  enrollment: {
    schema: () => ConfirmationSchema,
    backup: false,
    label: "Confirm authenticator",
    action: "Enable two-factor authentication",
    pending: "Enabling...",
  },
  regeneration: {
    schema: () => RegenerationSchema,
    backup: false,
    label: "Replace backup codes",
    action: "Generate new backup codes",
    pending: "Generating backup codes...",
  },
  turnOff: {
    schema: turnOffSchema,
    backup: true,
    label: "Confirm turning off two-factor authentication",
    action: "Turn off two-factor authentication",
    pending: "Turning off...",
  },
} as const;

export function ConfirmForm({
  purpose,
  pending,
  locked,
  error,
  onEdit,
  onConfirm,
}: {
  purpose: keyof typeof purposes;
  pending: boolean;
  locked: boolean;
  error: ApiError | null;
  onEdit: () => void;
  onConfirm: (code: string, reject: (err: ApiError) => void) => void;
}) {
  const config = purposes[purpose];
  const [kind, setKind] = useState<CodeKind>("authenticator");
  const format = codeFormats[kind];
  const groupSize = format.length / 2;
  const groups = [0, groupSize];
  const inputId = `${purpose}-code`;
  const inputRef = useRef<HTMLInputElement>(null);
  const focused = useRef(false);
  const kindChanged = useRef(false);
  const submitRef = useRef<HTMLButtonElement>(null);
  const form = useForm({
    defaultValues: { code: "" },
    validators: { onChange: config.schema(kind) },
    listeners: { onChange: onEdit },
    onSubmitInvalid: () => inputRef.current?.focus(),
    onSubmit: ({ value, formApi }) => {
      if (pending || locked) return;
      onConfirm(value.code, (err) => {
        const { fields } = formErrors(err, fieldNames);
        if (Object.keys(fields).length) formApi.setErrorMap({ onSubmit: { fields } });
      });
    },
  });
  const submitted = useSelector(form.store, (state) => state.submissionAttempts > 0);
  const inputs = error ? formErrors(error, fieldNames) : undefined;
  const codeRejected = Boolean(inputs?.fields.code);

  useEffect(() => {
    // Recovery removes the status-check button; return focus to the unlocked proof field.
    if (error && uncertainSession(error)) focused.current = false;
    if (locked || focused.current) return;
    focused.current = true;
    inputRef.current?.focus();
  }, [locked, error]);
  useEffect(() => {
    if (!error || pending || locked) return;
    (codeRejected ? inputRef.current : submitRef.current)?.focus();
  }, [error, pending, locked, codeRejected]);

  useEffect(() => {
    // The keyed input remounts with the new kind.
    if (!kindChanged.current) return;
    kindChanged.current = false;
    inputRef.current?.focus();
  }, [kind]);

  function changeKind(): void {
    onEdit();
    form.reset();
    kindChanged.current = true;
    setKind(kind === "authenticator" ? "backup" : "authenticator");
  }

  return (
    <form
      className="flex flex-col gap-6"
      aria-label={config.label}
      aria-busy={pending}
      noValidate
      onSubmit={(event) => {
        event.preventDefault();
        void form.handleSubmit();
      }}
    >
      <FieldGroup>
        <form.Field name="code">
          {(field) => {
            const errors = submitted || field.state.meta.isBlurred ? field.state.meta.errors : [];
            return (
              <Field data-invalid={errors.length > 0} data-disabled={pending || locked}>
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <FieldLabel htmlFor={inputId}>{format.label}</FieldLabel>
                  {config.backup && (
                    <Button
                      type="button"
                      variant="link"
                      className="min-h-11 w-fit whitespace-normal focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
                      disabled={pending || locked}
                      onClick={changeKind}
                    >
                      {kind === "authenticator" ? "Use a backup code" : "Use authenticator app"}
                    </Button>
                  )}
                </div>
                <InputOTP
                  key={kind}
                  ref={inputRef}
                  id={inputId}
                  name="code"
                  value={field.state.value}
                  onChange={field.handleChange}
                  onBlur={field.handleBlur}
                  maxLength={format.length}
                  pattern={format.pattern}
                  inputMode={kind === "authenticator" ? "numeric" : "text"}
                  autoComplete="one-time-code"
                  autoCapitalize="none"
                  spellCheck={false}
                  pasteTransformer={codePaste}
                  disabled={pending || locked}
                  aria-invalid={errors.length > 0}
                  aria-describedby={errors.length ? `${inputId}-error` : undefined}
                  containerClassName="max-w-full flex-wrap gap-2"
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
                <FieldError id={`${inputId}-error`} errors={errors} />
              </Field>
            );
          }}
        </form.Field>
      </FieldGroup>
      <Button
        ref={submitRef}
        type="submit"
        className="h-auto min-h-11 whitespace-normal focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
        disabled={pending || locked}
      >
        {pending ? config.pending : config.action}
      </Button>
      {error && (
        <div className="flex flex-col gap-2">
          <FieldError errors={[{ message: userMessage(error) }, ...(inputs?.unmatched ?? [])]} />
          {error.requestId && (
            <p className="text-sm text-muted-foreground">
              Request reference: <code className="break-all">{error.requestId}</code>
            </p>
          )}
        </div>
      )}
    </form>
  );
}
