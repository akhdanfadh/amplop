import { useEffect, useMemo, useRef, useState, type ReactElement, type ReactNode } from "react";
import { revalidateLogic, useSelector } from "@tanstack/react-form";
import { useQueryClient } from "@tanstack/react-query";
import { getGetSystemEmailQueryKey, useSaveSystemEmail } from "@/api/generated/inbok";
import type { SystemEmailSettings } from "@/api/generated/model";
import { uncertainMessage, uncertainOutcome, userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import { useAppForm } from "@/components/form/app-form";
import { useFormContext } from "@/components/form/form-context";
import { useFormRequest } from "@/components/form/request";
import { LeaveWarning } from "@/components/leave-warning";
import { RequestError } from "@/components/request-error";
import { Button } from "@/components/ui/button";
import { FieldDescription, FieldGroup, FieldLegend, FieldSet } from "@/components/ui/field";
import { useSessionEnd } from "@/features/auth/session-end";
import { followedPort, settingsSchema, settingsValues, usualPort } from "./settings-schema";
import {
  fieldText,
  formText,
  groupText,
  leaveText,
  lostSave,
  portHint,
  securityOptions,
} from "./text";

// The inputs this form renders; a refusal of another goes in the summary.
const settingsFields = [
  "host",
  "security",
  "port",
  "username",
  "password",
  "fromAddress",
  "fromName",
] as const;

// SettingsForm edits the system email settings. saved is the last read,
// absent before the first save; focusFirst gives its first field focus when
// it opens, as Edit settings opens it. onSaved runs after a save the server
// confirmed; onWrite after every save that may have replaced the settings,
// confirmed or not; onCancel, absent before the first save, closes it
// without saving.
export function SettingsForm({
  saved,
  focusFirst,
  onSaved,
  onWrite,
  onCancel,
}: {
  saved: SystemEmailSettings | undefined;
  focusFirst: boolean;
  onSaved: () => void;
  onWrite: () => void;
  onCancel?: () => void;
}): ReactElement {
  const client = useQueryClient();
  const sessionEnd = useSessionEnd();
  const ref = useRef<HTMLDivElement>(null);
  // The port Security last moved the field to, which it says while the
  // field still holds it.
  const [followedTo, setFollowedTo] = useState<string>();
  // What the form opened with: leaving after changing it asks first.
  const [start] = useState(() => settingsValues(saved));
  const save = useSaveSystemEmail({
    mutation: {
      // The request carries the password; do not keep it after the form closes.
      gcTime: 0,
      // A read that started before the save could land after it and replace
      // the saved settings with older ones.
      onMutate: () => client.cancelQueries({ queryKey: getGetSystemEmailQueryKey(), exact: true }),
      // Here, not in the form, so the cache is updated even if the page closes.
      onSuccess: (result) => {
        client.setQueryData(getGetSystemEmailQueryKey(), result);
        onWrite();
      },
      onError: (err) => {
        if (sessionEnd(err) || !uncertainOutcome(err)) return;
        // The settings may have been replaced: read what is saved now.
        onWrite();
        void client.invalidateQueries({ queryKey: getGetSystemEmailQueryKey() });
      },
    },
  });
  const request = useFormRequest(save, settingsFields);
  const schema = useMemo(() => settingsSchema(saved), [saved]);
  const form = useAppForm({
    defaultValues: start,
    validationLogic: revalidateLogic(),
    validators: { onDynamic: request.check(schema) },
    onSubmit: async ({ value, formApi }) => {
      const sent = await request.send(formApi, value, { data: schema.parse(value) });
      if (sent.outcome === "done") onSaved();
    },
    listeners: { onChange: () => save.reset() },
  });
  const security = useSelector(form.store, (state) => state.values.security);
  const port = useSelector(form.store, (state) => state.values.port);
  // Typing back what the form opened with counts as no change.
  const changed = useSelector(form.store, (state) => !state.isDefaultValue);

  useEffect(() => {
    if (focusFirst) ref.current?.querySelector<HTMLElement>("input")?.focus();
    // Only as the form opens.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div ref={ref} className="w-full">
      <LeaveWarning active={changed} text={leaveText} />
      <form.AppForm>
        <form.Form label="System email settings">
          <FieldGroup className="gap-8">
            <Group {...groupText.connection}>
              <form.AppField name="host">
                {(field) => (
                  <field.TextField {...fieldText.host} autoComplete="off" text="literal" />
                )}
              </form.AppField>
              <form.AppField
                name="security"
                listeners={{
                  onChange: ({ value }) => {
                    const current = form.getFieldValue("port");
                    const next = followedPort(value, current);
                    if (next === current) return;
                    setFollowedTo(next);
                    form.setFieldValue("port", next);
                  },
                }}
              >
                {(field) => <field.ChoiceField {...fieldText.security} options={securityOptions} />}
              </form.AppField>
              <form.AppField name="port">
                {(field) => (
                  <field.TextField
                    {...fieldText.port}
                    description={portHint(
                      security,
                      usualPort[security],
                      port === followedTo ? "changed" : "kept",
                    )}
                    autoComplete="off"
                    text="literal"
                    inputMode="numeric"
                    width="short"
                  />
                )}
              </form.AppField>
            </Group>
            <Group {...groupText.signIn}>
              <form.AppField name="username">
                {(field) => (
                  <field.TextField
                    {...fieldText.username}
                    optional
                    autoComplete="off"
                    text="literal"
                    passwordManagers="ignore"
                  />
                )}
              </form.AppField>
              <form.AppField name="password">
                {(field) => (
                  <field.PasswordField
                    label={fieldText.password.label}
                    toggle={fieldText.password.toggle}
                    description={
                      saved?.hasPassword ? fieldText.password.kept : fieldText.password.new
                    }
                    autoComplete="new-password"
                    passwordManagers="ignore"
                  />
                )}
              </form.AppField>
            </Group>
            <Group {...groupText.sender}>
              <form.AppField name="fromAddress">
                {(field) => <field.EmailField {...fieldText.fromAddress} autoComplete="off" />}
              </form.AppField>
              <form.AppField name="fromName">
                {(field) => <field.TextField {...fieldText.fromName} autoComplete="off" />}
              </form.AppField>
            </Group>
          </FieldGroup>
          {/* Only tested settings have a test to lose. */}
          {saved?.testedAt && (
            <p className="text-sm text-muted-foreground">{formText.untestedNote}</p>
          )}
          <div className="flex flex-wrap gap-3">
            <form.SubmitButton working={formText.saving}>{formText.save}</form.SubmitButton>
            {onCancel && <CancelButton onCancel={onCancel} />}
          </div>
          <RequestError errors={request.summary(saveMessage)} requestId={request.reference} />
        </form.Form>
      </form.AppForm>
    </div>
  );
}

// saveMessage words a failed save: a lost answer asks to save again, which
// is safe since a save replaces every setting.
function saveMessage(err: ApiError): string {
  return uncertainOutcome(err) ? uncertainMessage(err, lostSave) : userMessage(err);
}

// Group is a titled group of the form's fields, e.g. Connection.
function Group({
  title,
  about,
  children,
}: {
  title: string;
  about: string;
  children: ReactNode;
}): ReactElement {
  return (
    <FieldSet>
      <FieldLegend>{title}</FieldLegend>
      <FieldDescription>{about}</FieldDescription>
      <FieldGroup>{children}</FieldGroup>
    </FieldSet>
  );
}

// CancelButton closes the form without saving; it waits while a save sends.
function CancelButton({ onCancel }: { onCancel: () => void }): ReactElement {
  const form = useFormContext();
  const sending = useSelector(form.store, (state) => state.isSubmitting);
  return (
    <Button type="button" variant="outline" isDisabled={sending} onPress={onCancel}>
      {formText.cancel}
    </Button>
  );
}
