import { useEffect, useRef } from "react";
import { useForm, useSelector } from "@tanstack/react-form";
import { REGEXP_ONLY_DIGITS } from "input-otp";
import type { ApiError } from "@/api/transport";
import { formErrors, userMessage } from "@/api/problem";
import { uncertainSession } from "@/features/auth/session-error";
import { Button } from "@/components/ui/button";
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import {
  InputOTP,
  InputOTPGroup,
  InputOTPSeparator,
  InputOTPSlot,
} from "@/components/ui/input-otp";
import { authenticatorDigits, ConfirmationSchema, RegenerationSchema, pasteCode } from "./schema";

const fieldNames = ["code"] as const;
const halfDigits = authenticatorDigits / 2;
const indices = Array.from({ length: halfDigits }, (_, index) => index);
const purposes = {
  enrollment: {
    schema: ConfirmationSchema,
    label: "Confirm authenticator",
    action: "Enable two-factor authentication",
    pending: "Enabling...",
  },
  regeneration: {
    schema: RegenerationSchema,
    label: "Replace backup codes",
    action: "Generate new backup codes",
    pending: "Generating backup codes...",
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
  const inputId = `${purpose}-code`;
  const inputRef = useRef<HTMLInputElement>(null);
  const focused = useRef(false);
  const submitRef = useRef<HTMLButtonElement>(null);
  const form = useForm({
    defaultValues: { code: "" },
    validators: { onChange: config.schema },
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
                <FieldLabel htmlFor={inputId}>Authenticator code</FieldLabel>
                <InputOTP
                  ref={inputRef}
                  id={inputId}
                  name="code"
                  value={field.state.value}
                  onChange={field.handleChange}
                  onBlur={field.handleBlur}
                  maxLength={authenticatorDigits}
                  pattern={REGEXP_ONLY_DIGITS}
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  pasteTransformer={pasteCode}
                  disabled={pending || locked}
                  aria-invalid={errors.length > 0}
                  aria-describedby={errors.length ? `${inputId}-error` : undefined}
                  containerClassName="max-w-full gap-2"
                >
                  <InputOTPGroup className="min-w-0 flex-1">
                    {indices.map((index) => (
                      <InputOTPSlot key={index} index={index} aria-invalid={errors.length > 0} />
                    ))}
                  </InputOTPGroup>
                  <InputOTPSeparator />
                  <InputOTPGroup className="min-w-0 flex-1">
                    {indices.map((index) => (
                      <InputOTPSlot
                        key={index}
                        index={index + halfDigits}
                        aria-invalid={errors.length > 0}
                      />
                    ))}
                  </InputOTPGroup>
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
