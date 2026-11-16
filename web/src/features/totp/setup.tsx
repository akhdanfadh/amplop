import { useEffect, useRef, useState, type ReactElement, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { QRCodeSVG } from "qrcode.react";
import {
  getGetTOTPStatusQueryKey,
  useCancelTOTPEnrollment,
  useCheckTOTPEnrollmentCode,
  useFinishTOTPEnrollment,
  useGetTOTPStatus,
  useShowWaitingTOTPBackupCodes,
  useStartTOTPEnrollment,
} from "@/api/generated/inbok";
import type {
  ResponseTotpStatus,
  TotpEnrollment,
  TotpStatus,
  WaitingBackupCodes,
} from "@/api/generated/model";
import { uncertainOutcome, userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import { lineLink } from "@/components/link-style";
import { ReadError } from "@/components/read-error";
import { RequestError } from "@/components/request-error";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { resetSession } from "@/features/auth/session-cache";
import { cn } from "@/lib/cn";
import { CodeStep } from "./code-step";
import { CodesStep, notWaitingType } from "./codes-step";
import { PasswordStep } from "./password-step";
import { EnrollmentCodeSchema } from "./schema";
import {
  cancelText,
  checkingLabel,
  checkingStatus,
  continueLabel,
  expiredNotice,
  qrText,
  showCodesForm,
  startForm,
  startLead,
  statusReadFailed,
  waitingLead,
} from "./text";

const unauthorized = 401;
const conflict = 409;
// QR codes need a clear border of four modules for scanners (ISO/IEC 18004).
const qrQuietZone = 4;

// Where a setup stands, as this page shows it: reading the status first,
// the password before a secret shows, the QR code and the code check, the
// backup codes waiting for "I've saved these codes", or done.
export type SetupStage = "loading" | "error" | "password" | "qr" | "codes" | "done";

// What this page holds that the server shows only after the password: the
// setup's secret, or the codes waiting with their one-time token.
type Shown =
  | { kind: "secret"; enrollment: TotpEnrollment }
  | { kind: "codes"; codes: WaitingBackupCodes }
  | undefined;

// useSetupFlow sets up two-factor authentication for email's account: the
// password, the QR code and its first code, then the backup codes, until
// "I've saved these codes" turns it on. It follows the server's status, so
// a reload, or another tab, lands on the step the setup is at; only what
// the server shows after the password is held here. It returns the stage
// and its content, for the page to place, e.g. in a settings page.
// onFinished runs once two-factor is on here, after the session cookie it
// replaced is cleared from the cache; onCanceled once the setup is
// canceled; onError receives every request's failure, the status reads'
// included, e.g. for the page to stop on a 401. Pass a stable onError,
// e.g. from useCallback: a failed read is reported again when it changes.
export function useSetupFlow({
  email,
  onFinished,
  onCanceled,
  onError,
}: {
  email: string;
  onFinished: () => void;
  onCanceled: () => void;
  onError: (err: ApiError) => void;
}): { stage: SetupStage; content: ReactNode } {
  const client = useQueryClient();
  const [shown, setShown] = useState<Shown>();
  // Options on the hooks, not on mutate(): TanStack Query runs mutate()'s
  // callbacks only while this page is mounted, and a member may leave
  // while a request runs.
  const start = useStartTOTPEnrollment({ mutation: { gcTime: 0, onError } });
  const reveal = useShowWaitingTOTPBackupCodes({ mutation: { gcTime: 0, onError } });
  const check = useCheckTOTPEnrollmentCode({ mutation: { gcTime: 0, onError } });
  const finish = useFinishTOTPEnrollment({
    mutation: {
      gcTime: 0,
      // Finishing replaces this session's cookie: a read sent with the old
      // one now would find it ended.
      onMutate: () => client.cancelQueries(),
      // What the app read about the old session ended with it. Reset, not
      // removed as clearAccountCache does: a page already open, e.g.
      // Security after leaving mid-request, still watches its queries,
      // which then read afresh; a removed one would leave it waiting.
      onSuccess: () => void resetSession(client),
      onError,
    },
  });
  const cancel = useCancelTOTPEnrollment({ mutation: { gcTime: 0, onError } });
  const status = useGetTOTPStatus({
    query: { enabled: !finish.isPending, networkMode: "always" },
  });
  const stage = setupStage(status.data?.data, status.error, shown);
  // A read fails as a write does, e.g. with a 401 once the session ended,
  // also when an earlier read's status is still shown.
  useEffect(() => {
    if (status.error) onError(status.error);
  }, [status.error, onError]);
  // A status that changes to one asking for the password, e.g. the setup
  // expired, makes what this page holds stale: drop it. Only a change counts:
  // a step's own answer reaches this page a render before the status it
  // writes. Set while rendering, as React's "Adjusting some state when a
  // prop changes" does, so no render shows it.
  const [seen, setSeen] = useState(status.data);
  if (status.data !== seen) {
    setSeen(status.data);
    if (shown && setupStage(status.data?.data, null, shown) === "password") setShown(undefined);
  }

  // showStatus puts a write's known result in the cache, so the step moves
  // on at once instead of after a read.
  function showStatus(next: TotpStatus): void {
    client.setQueryData<ResponseTotpStatus>(getGetTOTPStatusQueryKey(), { data: next });
  }

  // recheck reads the status after a refusal that says the setup moved,
  // e.g. it expired, or after an answer that never came, which may have
  // changed it. A refused password or code, or a wait, changed nothing.
  function recheck(err: ApiError): void {
    if (err.status === conflict || uncertainOutcome(err)) void status.refetch();
  }

  function cancelSetup(): void {
    if (cancel.isPending) return;
    cancel.mutate(undefined, {
      onSuccess: (result) => {
        setShown(undefined);
        showStatus(result.data);
        onCanceled();
      },
      onError: recheck,
    });
  }

  // Canceling throws away a secret the app may already hold, so it asks
  // first (DESIGN.md "Feedback").
  const cancelButton = (
    <div className="flex flex-col items-start gap-2">
      <AlertDialogTrigger>
        <Button
          type="button"
          variant="outline"
          isPending={cancel.isPending}
          isDisabled={finish.isPending || check.isPending}
        >
          {cancel.isPending ? cancelText.working : cancelText.label}
        </Button>
        <AlertDialog>
          <AlertDialogHeader>
            <AlertDialogTitle>{cancelText.title}</AlertDialogTitle>
            <AlertDialogDescription>{cancelText.description}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{cancelText.keep}</AlertDialogCancel>
            <AlertDialogAction type="button" variant="destructive" onPress={cancelSetup}>
              {cancelText.label}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialog>
      </AlertDialogTrigger>
      {cancel.error && cancel.error.status !== unauthorized && (
        <RequestError
          errors={[{ message: userMessage(cancel.error) }]}
          requestId={uncertainOutcome(cancel.error) ? cancel.error.requestId : undefined}
        />
      )}
    </div>
  );

  let content: ReactNode = null;
  const current = status.data?.data;
  if (stage === "loading") content = <output>{checkingStatus}</output>;
  if (stage === "error" && status.error)
    content = (
      <ReadError
        message={`${statusReadFailed} ${userMessage(status.error)}`}
        error={status.error}
        retrying={status.isFetching}
        onRetry={() => void status.refetch()}
      />
    );
  if (stage === "password" && current?.backupCodesWaiting)
    content = (
      <PasswordStep
        email={email}
        label={showCodesForm}
        lead={waitingLead}
        mutation={reveal}
        onDone={(result) => setShown({ kind: "codes", codes: result.data })}
        onRefused={recheck}
      />
    );
  else if (stage === "password")
    content = (
      <PasswordStep
        // A new form after an expired setup: nothing typed carries over.
        key={current?.status}
        email={email}
        label={startForm}
        // The field takes focus, so what happened goes where a screen
        // reader reads it with the field.
        lead={current?.status === "expired" ? `${expiredNotice} ${startLead}` : startLead}
        mutation={start}
        onDone={(result) => {
          setShown({ kind: "secret", enrollment: result.data });
          showStatus({ status: "pending", backupCodesLeft: 0, backupCodesWaiting: false });
        }}
        onRefused={recheck}
      />
    );
  if (stage === "qr" && shown?.kind === "secret")
    content = (
      <QrStep enrollment={shown.enrollment}>
        <CodeStep
          label={qrText.form}
          action={continueLabel}
          working={checkingLabel}
          schema={EnrollmentCodeSchema}
          mutation={check}
          onDone={(result) => {
            setShown({ kind: "codes", codes: result.data });
            showStatus({ status: "pending", backupCodesLeft: 0, backupCodesWaiting: true });
          }}
          onRefused={recheck}
        />
        {cancelButton}
      </QrStep>
    );
  if (stage === "codes" && shown?.kind === "codes")
    content = (
      <CodesStep
        kind="setup"
        // The steps sit straight under the page's title.
        headingLevel={2}
        codes={shown.codes.backupCodes}
        saving={finish.isPending}
        error={finish.error?.status === unauthorized ? null : finish.error}
        // Canceling and finishing must not race: one ends the setup the
        // other needs.
        isDisabled={cancel.isPending}
        leaveWarning={!finish.isSuccess && !cancel.isSuccess}
        onSaved={() => {
          if (finish.isPending || cancel.isPending) return;
          finish.mutate(
            { data: { token: shown.codes.token } },
            {
              onSuccess: onFinished,
              onError: (err) => {
                // Another tab showed the codes again, which replaced this
                // token: show them again here too, after the password.
                if (err.status === conflict && err.problem?.type === notWaitingType)
                  setShown(undefined);
                recheck(err);
              },
            },
          );
        }}
      >
        {cancelButton}
      </CodesStep>
    );

  return { stage, content };
}

// setupStage returns where the setup stands: the status as read, and what
// this page was shown after the password, which reaches a step only while
// the status still allows it.
function setupStage(
  current: TotpStatus | undefined,
  error: ApiError | null,
  shown: Shown,
): SetupStage {
  if (!current) return error ? "error" : "loading";
  if (current.status === "on") return "done";
  if (current.status !== "pending") return "password";
  if (current.backupCodesWaiting) return shown?.kind === "codes" ? "codes" : "password";
  return shown?.kind === "secret" ? "qr" : "password";
}

// QrStep shows the setup's QR code and setup key, then children, the form
// for the first code from the app. Checking it leaves two-factor off.
function QrStep({
  enrollment,
  children,
}: {
  enrollment: TotpEnrollment;
  children: ReactNode;
}): ReactElement {
  const headingRef = useRef<HTMLHeadingElement>(null);
  const [message, setMessage] = useState("");

  // A new step: a screen reader starts from its first instruction. The code
  // field waits until the member has scanned.
  useEffect(() => {
    headingRef.current?.focus();
  }, []);

  async function copySecret(): Promise<void> {
    try {
      await navigator.clipboard.writeText(enrollment.secret);
      setMessage(qrText.copied);
    } catch {
      setMessage(qrText.copyFailed);
    }
  }

  return (
    <div className="flex w-full flex-col items-start gap-8">
      <div className="flex w-full flex-col items-start gap-3">
        {/* The steps sit straight under the page's title. */}
        <h2 ref={headingRef} tabIndex={-1} className="font-medium outline-none">
          {qrText.scan}
        </h2>
        {/* Centred under a signed-out card's centred title, and on a narrow
            settings page, where it nearly fills the width (@max-lg, below
            512 px of page, as sections stack). A wide settings page keeps
            the left edge of the text around it. */}
        <div className="w-fit rounded-md border bg-white p-1 in-data-[slot=card]:self-center @max-lg:self-center">
          <QRCodeSVG
            value={enrollment.uri}
            title={qrText.image}
            size={176}
            marginSize={qrQuietZone}
          />
        </div>
        {/* Only where the page is narrow or the pointer a finger, as on the
            phone that holds the app. Narrow is the width below which
            settings sections stack (@lg, 512 px of the settings page or the
            signed-out layout): the settings page caps at 720 px, so a wider
            breakpoint would never hide it there. */}
        <a href={enrollment.uri} className={cn(lineLink, "@lg:pointer-fine:hidden")}>
          {qrText.addToApp}
        </a>
        <p className="text-sm text-muted-foreground">{qrText.keyLead}</p>
        {/* Groups of four are easier to type; authenticator apps ignore the
            spaces. Copy gives the key without them. */}
        <code className="w-full rounded-md bg-muted px-2 py-1 font-mono text-sm wrap-break-word select-all">
          {enrollment.secret.match(/.{1,4}/g)?.join(" ")}
        </code>
        <div>
          <Button type="button" variant="outline" onPress={() => void copySecret()}>
            {qrText.copy}
          </Button>
          {/* Always present, so a screen reader announces the message that
              fills it; empty, it takes no space. */}
          <output className="block text-sm not-empty:mt-2">{message}</output>
        </div>
      </div>
      <div className="flex w-full flex-col items-start gap-3">
        <h2 className="font-medium">{qrText.enter}</h2>
        {children}
      </div>
    </div>
  );
}
