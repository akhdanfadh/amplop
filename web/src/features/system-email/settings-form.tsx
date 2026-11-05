import { useEffect, useMemo, useRef, useState, type ReactElement } from "react";
import { useForm, useSelector, type AnyFieldMeta } from "@tanstack/react-form";
import { useIsMutating, useQueryClient, type QueryObserverResult } from "@tanstack/react-query";
import {
  getGetSystemEmailQueryKey,
  getSendSystemEmailTestMessageMutationKey,
  useSaveSystemEmail,
} from "@/api/generated/inbok";
import type { ResponseSystemEmail, SystemEmailSettings } from "@/api/generated/model";
import type { ApiError } from "@/api/transport";
import { formErrors, formSummary, uncertainOutcome, userMessage } from "@/api/problem";
import { PasswordInput } from "@/components/password-input";
import { RequestError } from "@/components/request-error";
import { Button } from "@/components/ui/button";
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { blurKeepingSubmitErrors } from "@/lib/blur-keeping-submit-errors";
import {
  checkedSave,
  settingsSchema,
  settingsValues,
  type SettingsValues,
} from "./settings-schema";

// Only these inputs are rendered; other rejected inputs belong in the summary.
const settingsFields = [
  "host",
  "port",
  "security",
  "username",
  "password",
  "fromAddress",
  "fromName",
] satisfies (keyof SettingsValues)[];

const securityOptions = [
  {
    value: "tls",
    label: "TLS",
    description: "Encrypted from the start. Usually port 465.",
  },
  {
    value: "starttls",
    label: "STARTTLS",
    description: "Switches to an encrypted connection before signing in. Usually port 587.",
  },
  {
    value: "none",
    label: "None",
    description: "Not encrypted, so it can't sign in. For a local test server only.",
  },
] as const satisfies { value: SettingsValues["security"]; label: string; description: string }[];

const savedMessage = "Settings saved.";
const checkedMessages = {
  saved: savedMessage,
  passwordUnconfirmed:
    "The other settings are saved, but the password can't be read back. Save again to make sure it's saved.",
  different: "The saved settings don't match this form. Save to replace them.",
};

const inputClass =
  "h-11 border-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid dark:aria-invalid:border-destructive";
// Password managers ignore autocomplete="off"; these keep them from filling
// in, or saving, the inbok sign-in as the mail server's account.
const notInbokSignIn = {
  "data-1p-ignore": true,
  "data-lpignore": "true",
  "data-bwignore": true,
  "data-form-type": "other",
};
const buttonClass =
  "h-auto min-h-11 w-fit whitespace-normal focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid";

// SettingsForm replaces the system email settings. saved is the last read,
// absent before the first save; onCheck reads them again; onSaved runs when
// a save may have replaced them; onAccessError receives every save error.
export function SettingsForm({
  saved,
  onCheck,
  onSaved,
  onAccessError,
}: {
  saved: SystemEmailSettings | undefined;
  onCheck: () => Promise<QueryObserverResult<ResponseSystemEmail, ApiError>>;
  onSaved: () => void;
  onAccessError: (err: ApiError) => void;
}): ReactElement {
  const client = useQueryClient();
  const formRef = useRef<HTMLFormElement>(null);
  const checkRef = useRef<HTMLButtonElement>(null);
  const saveRef = useRef<HTMLButtonElement>(null);
  // Set when a save or check starts; focus moves once it ends.
  const refocus = useRef(false);
  const [checking, setChecking] = useState(false);
  const [checked, setChecked] = useState<keyof typeof checkedMessages>();
  const [checkError, setCheckError] = useState<ApiError>();
  // A test reads the saved settings, so a save waits for it to finish.
  const testing = useIsMutating({ mutationKey: getSendSystemEmailTestMessageMutationKey() }) > 0;
  const save = useSaveSystemEmail({
    mutation: {
      // The request carries the password; do not keep it after the form closes.
      gcTime: 0,
      // A read that started before the save could land after it and replace
      // the saved settings with older ones.
      onMutate: () => client.cancelQueries({ queryKey: getGetSystemEmailQueryKey(), exact: true }),
      // Here, not in mutate(), so the cache is updated even if the page closes.
      onSuccess: (result) => {
        client.setQueryData(getGetSystemEmailQueryKey(), result);
        onSaved();
      },
      onError: (err) => {
        onAccessError(err);
        // The settings may have been replaced.
        if (uncertainOutcome(err)) onSaved();
      },
    },
  });
  const uncertain = Boolean(save.error && uncertainOutcome(save.error));
  const locked = save.isPending || uncertain || checking;
  const schema = useMemo(() => settingsSchema(saved), [saved]);

  const form = useForm({
    defaultValues: settingsValues(saved),
    validators: { onChange: schema },
    onSubmitInvalid: ({ formApi }) =>
      focusFirstInvalid(formRef.current, (name) => formApi.getFieldMeta(name)?.errors.length),
    onSubmit: ({ value, formApi }) => {
      if (locked || testing) return;
      setChecked(undefined);
      refocus.current = true;
      save.mutate(
        { data: value },
        {
          onSuccess: (result) => formApi.reset(settingsValues(result.data.settings)),
          // Only input errors enter form state, so request failures allow another submit.
          onError: (err) => {
            const { fields } = formErrors(err, settingsFields);
            if (Object.keys(fields).length > 0) formApi.setErrorMap({ onSubmit: { fields } });
          },
        },
      );
    },
    listeners: {
      onChange: () => {
        // Resetting an idle mutation still re-renders the whole form.
        if (!save.isIdle) save.reset();
        setChecked(undefined);
      },
    },
  });
  const submitted = useSelector(form.store, (state) => state.submissionAttempts > 0);
  const summary = save.error
    ? formSummary(userMessage(save.error), formErrors(save.error, settingsFields))
    : [];

  // Once a save or check ends, focus what needs attention next.
  useEffect(() => {
    if (!refocus.current || save.isPending || checking) return;
    refocus.current = false;
    if (uncertain) checkRef.current?.focus();
    else if (!focusFirstInvalid(formRef.current, (name) => form.getFieldMeta(name)?.errors.length))
      saveRef.current?.focus();
  });

  // Reads the settings again after an uncertain save, then tells whether
  // they match the form before allowing another save.
  async function check(): Promise<void> {
    setCheckError(undefined);
    setChecking(true);
    refocus.current = true;
    const result = await onCheck();
    setChecking(false);
    // A failed read still returns the older cached settings; they prove nothing.
    if (!result.isSuccess) {
      setCheckError(result.error ?? undefined);
      return;
    }
    const outcome = checkedSave(result.data.data.settings, form.state.values);
    save.reset();
    setChecked(outcome);
    if (outcome === "saved") form.reset(settingsValues(result.data.data.settings));
  }

  // A field's errors once the user has left it or tried to submit.
  function shown(meta: AnyFieldMeta): AnyFieldMeta["errors"] {
    return meta.isBlurred || submitted ? meta.errors : [];
  }

  return (
    <form
      ref={formRef}
      className="flex flex-col gap-6"
      noValidate
      aria-label="System email settings"
      aria-busy={save.isPending}
      onSubmit={(event) => {
        event.preventDefault();
        void form.handleSubmit();
      }}
    >
      <FieldGroup>
        <form.Field name="host">
          {(field) => {
            const errors = shown(field.state.meta);
            const id = "system-email-host";
            return (
              <Field data-invalid={errors.length > 0}>
                <FieldLabel htmlFor={id}>Mail server</FieldLabel>
                <Input
                  id={id}
                  name={field.name}
                  className={inputClass}
                  placeholder="smtp.example.com"
                  autoComplete="off"
                  autoCapitalize="none"
                  spellCheck={false}
                  readOnly={locked}
                  aria-invalid={errors.length > 0}
                  aria-describedby={describedBy(id, errors.length > 0)}
                  value={field.state.value}
                  onBlur={() => blurKeepingSubmitErrors(field)}
                  onChange={(event) => field.handleChange(event.target.value)}
                />
                <FieldDescription id={`${id}-description`}>
                  The SMTP host name from your email provider.
                </FieldDescription>
                <FieldError id={`${id}-error`} errors={errors} />
              </Field>
            );
          }}
        </form.Field>

        <form.Field name="port">
          {(field) => {
            const errors = shown(field.state.meta);
            const id = "system-email-port";
            return (
              <Field data-invalid={errors.length > 0}>
                <FieldLabel htmlFor={id}>Port</FieldLabel>
                <Input
                  id={id}
                  name={field.name}
                  className={`${inputClass} w-32`}
                  type="number"
                  inputMode="numeric"
                  autoComplete="off"
                  readOnly={locked}
                  aria-invalid={errors.length > 0}
                  aria-describedby={describedBy(id, errors.length > 0)}
                  // An emptied box gives NaN; show it as empty, not "NaN".
                  value={Number.isNaN(field.state.value) ? "" : field.state.value}
                  onBlur={() => blurKeepingSubmitErrors(field)}
                  onChange={(event) => field.handleChange(event.target.valueAsNumber)}
                />
                <FieldDescription id={`${id}-description`}>
                  Usually 465 for TLS or 587 for STARTTLS.
                </FieldDescription>
                <FieldError id={`${id}-error`} errors={errors} />
              </Field>
            );
          }}
        </form.Field>

        <form.Field name="security">
          {(field) => {
            const errors = shown(field.state.meta);
            const id = "system-email-security";
            return (
              <FieldSet data-invalid={errors.length > 0}>
                <FieldLegend id={`${id}-legend`} variant="label">
                  Security
                </FieldLegend>
                <RadioGroup
                  name={field.name}
                  value={field.state.value}
                  readOnly={locked}
                  aria-labelledby={`${id}-legend`}
                  aria-invalid={errors.length > 0}
                  aria-describedby={errors.length > 0 ? `${id}-error` : undefined}
                  onValueChange={(value) => {
                    // Base UI types the value as unknown; keep only an offered mode.
                    const option = securityOptions.find((offered) => offered.value === value);
                    if (option) field.handleChange(option.value);
                  }}
                >
                  {securityOptions.map((option) => (
                    <Field key={option.value} orientation="horizontal">
                      <RadioGroupItem
                        className="border-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
                        id={`${id}-${option.value}`}
                        value={option.value}
                        // Focus lands on a radio, not the group, so each names the error.
                        aria-describedby={
                          errors.length > 0
                            ? `${id}-${option.value}-description ${id}-error`
                            : `${id}-${option.value}-description`
                        }
                        onBlur={() => blurKeepingSubmitErrors(field)}
                      />
                      <FieldContent>
                        <FieldLabel htmlFor={`${id}-${option.value}`}>{option.label}</FieldLabel>
                        <FieldDescription id={`${id}-${option.value}-description`}>
                          {option.description}
                        </FieldDescription>
                      </FieldContent>
                    </Field>
                  ))}
                </RadioGroup>
                <FieldError id={`${id}-error`} errors={errors} />
              </FieldSet>
            );
          }}
        </form.Field>

        <form.Field name="username">
          {(field) => {
            const errors = shown(field.state.meta);
            const id = "system-email-username";
            return (
              <Field data-invalid={errors.length > 0}>
                <FieldLabel htmlFor={id}>Username</FieldLabel>
                <Input
                  id={id}
                  name={field.name}
                  className={inputClass}
                  autoComplete="off"
                  {...notInbokSignIn}
                  autoCapitalize="none"
                  spellCheck={false}
                  readOnly={locked}
                  aria-invalid={errors.length > 0}
                  aria-describedby={describedBy(id, errors.length > 0)}
                  value={field.state.value}
                  onBlur={() => blurKeepingSubmitErrors(field)}
                  onChange={(event) => field.handleChange(event.target.value)}
                />
                <FieldDescription id={`${id}-description`}>
                  Leave empty if the mail server doesn't need a sign-in.
                </FieldDescription>
                <FieldError id={`${id}-error`} errors={errors} />
              </Field>
            );
          }}
        </form.Field>

        <form.Field name="password">
          {(field) => {
            const errors = shown(field.state.meta);
            const id = "system-email-password";
            return (
              <Field data-invalid={errors.length > 0}>
                <FieldLabel htmlFor={id}>Password</FieldLabel>
                <PasswordInput
                  id={id}
                  name={field.name}
                  label="mail server password"
                  // Browsers fill a saved password into "current-password" fields.
                  autoComplete="new-password"
                  {...notInbokSignIn}
                  autoCapitalize="none"
                  spellCheck={false}
                  readOnly={locked}
                  aria-invalid={errors.length > 0}
                  aria-describedby={describedBy(id, errors.length > 0)}
                  value={field.state.value}
                  onBlur={() => blurKeepingSubmitErrors(field)}
                  onChange={(event) => field.handleChange(event.target.value)}
                />
                <FieldDescription id={`${id}-description`}>
                  {saved?.hasPassword
                    ? "Leave empty to keep the saved password. Changing the mail server, port, or username needs it again."
                    : "The password for this username. Some providers call it an app password."}
                </FieldDescription>
                <FieldError id={`${id}-error`} errors={errors} />
              </Field>
            );
          }}
        </form.Field>

        <form.Field name="fromAddress">
          {(field) => {
            const errors = shown(field.state.meta);
            const id = "system-email-from-address";
            return (
              <Field data-invalid={errors.length > 0}>
                <FieldLabel htmlFor={id}>From address</FieldLabel>
                <Input
                  id={id}
                  name={field.name}
                  className={inputClass}
                  type="email"
                  placeholder="inbok@example.com"
                  autoComplete="off"
                  autoCapitalize="none"
                  spellCheck={false}
                  readOnly={locked}
                  aria-invalid={errors.length > 0}
                  aria-describedby={describedBy(id, errors.length > 0)}
                  value={field.state.value}
                  onBlur={() => blurKeepingSubmitErrors(field)}
                  onChange={(event) => field.handleChange(event.target.value)}
                />
                <FieldDescription id={`${id}-description`}>
                  The address inbok sends from. Your provider must allow this account to use it.
                </FieldDescription>
                <FieldError id={`${id}-error`} errors={errors} />
              </Field>
            );
          }}
        </form.Field>

        <form.Field name="fromName">
          {(field) => {
            const errors = shown(field.state.meta);
            const id = "system-email-from-name";
            return (
              <Field data-invalid={errors.length > 0}>
                <FieldLabel htmlFor={id}>From name</FieldLabel>
                <Input
                  id={id}
                  name={field.name}
                  className={inputClass}
                  autoComplete="off"
                  readOnly={locked}
                  aria-invalid={errors.length > 0}
                  aria-describedby={describedBy(id, errors.length > 0)}
                  value={field.state.value}
                  onBlur={() => blurKeepingSubmitErrors(field)}
                  onChange={(event) => field.handleChange(event.target.value)}
                />
                <FieldDescription id={`${id}-description`}>
                  The sender name recipients see.
                </FieldDescription>
                <FieldError id={`${id}-error`} errors={errors} />
              </Field>
            );
          }}
        </form.Field>
      </FieldGroup>

      <p className="text-sm text-muted-foreground">
        Saving marks the settings as untested until a test message passes.
      </p>

      {/* Disabled while saving, so a second click or Enter sends nothing.
          Inputs are read-only instead, so they keep focus and stay readable. */}
      <Button ref={saveRef} className={buttonClass} type="submit" disabled={locked || testing}>
        {save.isPending ? "Saving settings..." : "Save settings"}
      </Button>

      {/* The last attempt's error. Kept out of the form's own errors,
          which mean "these values are wrong" and block submitting until an
          edit. */}
      <RequestError errors={summary} requestId={save.error?.requestId} className="gap-3">
        {uncertain && (
          <Button
            ref={checkRef}
            type="button"
            variant="outline"
            className={`${buttonClass} border-muted-foreground`}
            disabled={checking}
            onClick={() => void check()}
          >
            {checking ? "Checking saved settings..." : "Check saved settings"}
          </Button>
        )}
      </RequestError>
      {checkError && (
        <p role="alert">Could not check the saved settings. {userMessage(checkError)}</p>
      )}
      <output aria-atomic="true">
        {save.isSuccess && savedMessage}
        {checked && checkedMessages[checked]}
      </output>
    </form>
  );
}

// The ids that describe a field: its description, then its errors if shown.
function describedBy(id: string, invalid: boolean): string {
  return invalid ? `${id}-description ${id}-error` : `${id}-description`;
}

// focusFirstInvalid focuses the first field in element that errorCount
// reports errors for, and returns whether it found one.
function focusFirstInvalid(
  element: HTMLFormElement | null,
  errorCount: (name: keyof SettingsValues) => number | undefined,
): boolean {
  const name = settingsFields.find((field) => errorCount(field));
  if (!name) return false;
  // Base UI names a hidden input for the radios; the checked radio takes focus.
  const selector = name === "security" ? '[role="radio"][aria-checked="true"]' : `[name="${name}"]`;
  element?.querySelector<HTMLElement>(selector)?.focus();
  return true;
}
