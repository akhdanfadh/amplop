import { useEffect, useRef, type ReactElement } from "react";
import { revalidateLogic } from "@tanstack/react-form";
import { useQueryClient } from "@tanstack/react-query";
import { CircleAlertIcon } from "lucide-react";
import {
  getGetRegistrationSettingsQueryKey,
  getGetSystemEmailQueryKey,
  useSendSystemEmailTestMessage,
} from "@/api/generated/inbok";
import { SystemEmailTestMessageRecipient, type SystemEmailSettings } from "@/api/generated/model";
import { emailAddress } from "@/api/validation";
import { useAppForm } from "@/components/form/app-form";
import { useFormRequest } from "@/components/form/request";
import { RequestError } from "@/components/request-error";
import { testErrorMessage, testResultMessage } from "./test-result";
import { testText } from "./text";
import { dayAndTime } from "@/lib/datetime";

const recipientFields = ["recipient"] as const;
const recipientSchema = SystemEmailTestMessageRecipient.extend({
  recipient: emailAddress(SystemEmailTestMessageRecipient.shape.recipient),
});

// TestMessageForm sends a test message with the saved settings, absent
// before the first save. recipient is the address to start with. editing
// says the settings form is open: the test waits, since it would test the
// saved settings, not the ones shown. savedAt is when a save last may have
// replaced the settings, in milliseconds, which hides an older result.
// sendRequests counts the times "Save and send test" asked for a test:
// each new count sends one, as pressing Send test message would.
export function TestMessageForm({
  settings,
  recipient,
  editing,
  savedAt,
  sendRequests,
}: {
  settings: SystemEmailSettings | undefined;
  recipient: string;
  editing: boolean;
  savedAt: number;
  sendRequests: number;
}): ReactElement {
  const client = useQueryClient();
  const ref = useRef<HTMLDivElement>(null);
  const send = useSendSystemEmailTestMessage({
    mutation: {
      // A pass records the tested time and a failure clears it; a lost
      // response may have done either. Either resumes or pauses an open
      // registration, so Registration reads its status again too.
      onSettled: () => {
        void client.invalidateQueries({ queryKey: getGetRegistrationSettingsQueryKey() });
        return client.invalidateQueries({ queryKey: getGetSystemEmailQueryKey() });
      },
    },
  });
  const request = useFormRequest(send, recipientFields);
  const form = useAppForm({
    defaultValues: { recipient },
    validationLogic: revalidateLogic(),
    validators: { onDynamic: request.check(recipientSchema) },
    onSubmit: async ({ value, formApi }) => {
      await request.send(formApi, value, { data: value });
    },
    listeners: { onChange: () => send.reset() },
  });
  // A result describes the settings it tested, so a later save hides it.
  const current = send.submittedAt > savedAt;
  const result =
    current && send.data && send.variables
      ? { ...send.data.data, recipient: send.variables.data.recipient }
      : undefined;
  const failed = result?.status === "failed";

  // "Save and send test" asks once the settings form has closed. It submits
  // as pressing the button does, through the kit's form, which moves focus
  // to an invalid recipient afterwards; until then the button has it and
  // says it is sending.
  useEffect(() => {
    if (sendRequests === 0) return;
    const button = ref.current?.querySelector<HTMLButtonElement>('[type="submit"]');
    button?.focus();
    button?.form?.requestSubmit(button);
    // Only for a new request.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sendRequests]);

  return (
    <div ref={ref} className="w-full">
      <form.AppForm>
        <form.Form label="Send a test message">
          <div className="flex flex-col gap-1">
            <p className="text-sm font-medium">
              <TestedStatus settings={settings} />
            </p>
            {settings && editing && (
              <p className="text-sm text-muted-foreground">{testText.waiting}</p>
            )}
          </div>
          <form.AppField name="recipient">
            {(field) => <field.EmailField {...testText.recipient} autoComplete="email" />}
          </form.AppField>
          <div>
            <form.SubmitButton working={testText.sending} isDisabled={!settings || editing}>
              {testText.send}
            </form.SubmitButton>
          </div>
          <RequestError
            errors={current ? request.summary(testErrorMessage) : []}
            requestId={current ? request.reference : undefined}
          />
          <output aria-atomic="true" className="text-sm empty:hidden">
            {result &&
              (failed ? (
                <span className="flex gap-1.5 text-destructive">
                  <CircleAlertIcon aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
                  {testResultMessage(result, result.recipient)}
                </span>
              ) : (
                testResultMessage(result, result.recipient)
              ))}
          </output>
        </form.Form>
      </form.AppForm>
    </div>
  );
}

// TestedStatus says whether the saved settings have passed a test.
function TestedStatus({ settings }: { settings: SystemEmailSettings | undefined }): ReactElement {
  if (!settings) return <>{testText.noSettings}</>;
  if (!settings.testedAt) return <>{testText.untested}</>;
  return (
    <>
      {testText.passed}{" "}
      <time dateTime={settings.testedAt}>{dayAndTime.format(new Date(settings.testedAt))}</time>.
    </>
  );
}
