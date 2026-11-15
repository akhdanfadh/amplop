import { useEffect, useRef, useState, type ReactElement } from "react";
import { revalidateLogic } from "@tanstack/react-form";
import { useQueryClient } from "@tanstack/react-query";
import {
  getGetCurrentSessionQueryKey,
  getGetTOTPStatusQueryKey,
  useDisableTOTP,
  useFinishTOTPBackupCodeReplacement,
  useGetTOTPStatus,
  useReplaceTOTPBackupCodes,
} from "@/api/generated/inbok";
import type {
  CurrentSession,
  ResponseTotpStatus,
  TotpStatus,
  WaitingBackupCodes,
} from "@/api/generated/model";
import { uncertainOutcome, userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import { cn } from "@/lib/cn";
import { useAppForm } from "@/components/form/app-form";
import { useFormRequest } from "@/components/form/request";
import { smallTarget } from "@/components/link-style";
import { RequestError } from "@/components/request-error";
import { Button } from "@/components/ui/button";
import { FieldGroup } from "@/components/ui/field";
import { codeFormats, codePaste, type CodeKind } from "@/features/auth/code-schema";
import { SettingsSection } from "@/features/settings/page";
import { CodeStep } from "./code-step";
import { CodesStep, notWaitingType } from "./codes-step";
import { ReplacementCodeSchema, turnOffSchema } from "./schema";
import {
  backupText,
  checkingLabel,
  checkingStatus,
  codesLeft,
  continueLabel,
  turnOffText,
} from "./text";

const conflict = 409;
const forbidden = 403;
const codeFields = ["code"] as const;

// Which of the page's sections is open; one at a time is.
export type OpenSection = "none" | "backup" | "turnOff";

// What the backup codes section shows: the count, the form for a fresh
// code, or the new codes.
type Replacing = "idle" | "code" | "codes";

// BackupCodesSection shows how many backup codes are left and replaces
// them: a fresh authenticator code makes a new set, which waits, while the
// old one still works, until "I've saved these codes". The new codes show
// only in that answer; leaving them unsaved means making another set.
// status is the read status, and onStale asks for it again; open says
// which section is open, onOpen opens or closes this one; onError receives
// every request's failure; onReplaced reports the new set active.
export function BackupCodesSection({
  status,
  open,
  onOpen,
  onError,
  onStale,
  onReplaced,
}: {
  status: TotpStatus;
  open: OpenSection;
  onOpen: (open: OpenSection) => void;
  onError: (err: ApiError) => void;
  onStale: () => void;
  onReplaced: () => void;
}): ReactElement {
  const client = useQueryClient();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const [replacing, setReplacing] = useState<Replacing>("idle");
  const [codes, setCodes] = useState<WaitingBackupCodes>();
  const replace = useReplaceTOTPBackupCodes({ mutation: { gcTime: 0, onError } });
  const finish = useFinishTOTPBackupCodeReplacement({ mutation: { gcTime: 0, onError } });
  // The page says which section is open; this one's step counts only then.
  const step = open === "backup" ? replacing : "idle";

  function go(next: Replacing): void {
    setReplacing(next);
    onOpen(next === "idle" ? "none" : "backup");
  }

  function close(): void {
    go("idle");
    setCodes(undefined);
    replace.reset();
    finish.reset();
    // The button that had focus is gone.
    headingRef.current?.focus();
  }

  function recheck(err: ApiError): void {
    if (err.status === conflict || uncertainOutcome(err)) onStale();
  }

  function showCodes(next: WaitingBackupCodes): void {
    setCodes(next);
    go("codes");
    client.setQueryData<ResponseTotpStatus>(getGetTOTPStatusQueryKey(), {
      data: { ...status, backupCodesWaiting: true },
    });
  }

  return (
    <SettingsSection
      title={backupText.title}
      about={backupText.about}
      layout={step === "idle" ? "columns" : "stacked"}
      headingRef={headingRef}
    >
      {step === "idle" && (
        <>
          <p>{codesLeft(status)}</p>
          <Button
            type="button"
            variant="outline"
            isDisabled={open === "turnOff"}
            onPress={() => go("code")}
          >
            {backupText.replace}
          </Button>
        </>
      )}
      {step === "code" && (
        <>
          <p className="text-sm text-muted-foreground">{backupText.replaceLead}</p>
          <CodeStep
            label={backupText.replace}
            action={continueLabel}
            working={checkingLabel}
            schema={ReplacementCodeSchema}
            mutation={replace}
            focusOnShow
            onDone={(result) => showCodes(result.data)}
            onRefused={recheck}
          >
            <Button type="button" variant="outline" isDisabled={replace.isPending} onPress={close}>
              {backupText.keep}
            </Button>
          </CodeStep>
        </>
      )}
      {step === "codes" && codes && (
        <CodesStep
          kind="replace"
          // Under this section's heading.
          headingLevel={3}
          codes={codes.backupCodes}
          saving={finish.isPending}
          error={finish.error}
          leaveWarning={!finish.isSuccess}
          onSaved={() => {
            if (finish.isPending) return;
            finish.mutate(
              { data: { token: codes.token } },
              {
                onSuccess: (result) => {
                  client.setQueryData<ResponseTotpStatus>(getGetTOTPStatusQueryKey(), result);
                  close();
                  onReplaced();
                },
                onError: (err) => {
                  // Another tab made new codes, which replaced this token.
                  if (err.status === conflict && err.problem?.type === notWaitingType) {
                    setCodes(undefined);
                    go("idle");
                  }
                  recheck(err);
                },
              },
            );
          }}
        />
      )}
    </SettingsSection>
  );
}

// TurnOffSection turns two-factor authentication off with a code, which
// confirms it: no dialog asks again (DESIGN.md "Feedback"). Once off, the
// status the answer holds takes the page back to Security. Administrators
// must keep it on, so they see why instead. open and onOpen are as for
// [BackupCodesSection]; onError receives the request's failure.
export function TurnOffSection({
  role,
  open,
  onOpen,
  onError,
  onStale,
}: {
  role: CurrentSession["role"];
  open: OpenSection;
  onOpen: (open: OpenSection) => void;
  onError: (err: ApiError) => void;
  onStale: () => void;
}): ReactElement {
  const client = useQueryClient();
  const headingRef = useRef<HTMLHeadingElement>(null);
  // Whether the form was open at the last commit, so a role change that
  // removes it moves focus to the heading, not the page's body.
  const formWasOpen = useRef(false);

  useEffect(() => {
    if (role === "member") {
      formWasOpen.current = open === "turnOff";
      return;
    }
    if (!formWasOpen.current) return;
    formWasOpen.current = false;
    headingRef.current?.focus();
  }, [role, open]);

  if (role !== "member")
    return (
      // Why it stays on, in place of what turning off does.
      <SettingsSection
        title={turnOffText.title}
        about={turnOffText.adminReason}
        headingRef={headingRef}
      >
        {null}
      </SettingsSection>
    );

  return (
    <SettingsSection
      title={turnOffText.title}
      about={turnOffText.about}
      layout={open === "turnOff" ? "stacked" : "columns"}
      headingRef={headingRef}
    >
      {open === "turnOff" ? (
        <TurnOffForm
          onClose={() => {
            onOpen("none");
            headingRef.current?.focus();
          }}
          onError={onError}
          onRefused={(err) => {
            // The role may have changed since this page read the session;
            // the page reads it live.
            if (err.status === forbidden)
              void client.invalidateQueries({ queryKey: getGetCurrentSessionQueryKey() });
            if (err.status === conflict || uncertainOutcome(err)) onStale();
          }}
        />
      ) : (
        <Button
          type="button"
          variant="destructive"
          isDisabled={open === "backup"}
          onPress={() => onOpen("turnOff")}
        >
          {turnOffText.action}
        </Button>
      )}
    </SettingsSection>
  );
}

// TurnOffForm asks for an authenticator code, or a backup code, and turns
// two-factor authentication off with it, putting the answer's status in the
// cache. onError receives every failure, onRefused one the form shows.
function TurnOffForm({
  onClose,
  onError,
  onRefused,
}: {
  onClose: () => void;
  onError: (err: ApiError) => void;
  onRefused: (err: ApiError) => void;
}): ReactElement {
  const client = useQueryClient();
  const fieldsRef = useRef<HTMLDivElement>(null);
  const [kind, setKind] = useState<CodeKind>("authenticator");
  const format = codeFormats[kind];
  const schema = turnOffSchema(kind);
  const disable = useDisableTOTP({ mutation: { gcTime: 0, onError } });
  const request = useFormRequest(disable, codeFields);
  const status = useGetTOTPStatus({ query: { networkMode: "always" } });
  const form = useAppForm({
    defaultValues: { code: "" },
    validationLogic: revalidateLogic(),
    validators: { onDynamic: request.check(schema) },
    onSubmit: async ({ value, formApi }) => {
      const sent = await request.send(formApi, value, { data: schema.parse(value) });
      if (sent.outcome === "done") {
        formApi.reset();
        client.setQueryData<ResponseTotpStatus>(getGetTOTPStatusQueryKey(), sent.data);
      }
      if (sent.outcome === "refused") onRefused(sent.error);
    },
    listeners: { onChange: () => disable.reset() },
  });

  // The member asked for this form, and on a switch to the other kind the
  // code goes in the new field.
  useEffect(() => {
    fieldsRef.current?.querySelector("input")?.focus();
  }, [kind]);

  return (
    <form.AppForm>
      <form.Form label={turnOffText.action} className="w-full max-w-sm">
        <p className="text-sm">{turnOffText.lead}</p>
        <FieldGroup ref={fieldsRef}>
          {/* A new kind is a new field, with nothing typed for the old one. */}
          <form.AppField key={kind} name="code">
            {(field) => (
              <field.CodeField
                label={format.label}
                length={format.length}
                pattern={format.pattern}
                inputMode={format.inputMode}
                paste={codePaste}
                labelAction={
                  <Button
                    type="button"
                    variant="link"
                    className={cn(smallTarget, "p-0 font-normal whitespace-normal")}
                    isDisabled={disable.isPending}
                    onPress={() => {
                      disable.reset();
                      form.reset();
                      setKind(kind === "authenticator" ? "backup" : "authenticator");
                    }}
                  >
                    {kind === "authenticator"
                      ? turnOffText.useBackup
                      : turnOffText.useAuthenticator}
                  </Button>
                }
              />
            )}
          </form.AppField>
        </FieldGroup>
        <div className="flex flex-wrap gap-3">
          <form.SubmitButton working={turnOffText.working} variant="destructive">
            {turnOffText.action}
          </form.SubmitButton>
          <Button type="button" variant="outline" isDisabled={disable.isPending} onPress={onClose}>
            {turnOffText.keep}
          </Button>
        </div>
        <RequestError errors={request.summary(message)} requestId={request.reference} />
      </form.Form>
    </form.AppForm>
  );

  // message words err. A lost answer makes the page reread the status, and
  // this form shows only while it reads on, so after the reread the answer
  // is known: still on. A failed reread leaves it unknown.
  function message(err: ApiError): string {
    if (!uncertainOutcome(err) || status.error) return userMessage(err);
    return status.isFetching ? checkingStatus : turnOffText.stillOn;
  }
}
