import { useEffect, useRef, useState } from "react";
import { useBlocker, useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { QRCodeSVG } from "qrcode.react";
import {
  getGetCurrentSessionQueryKey,
  getGetTOTPStatusQueryKey,
  getListSessionsQueryKey,
  useGetTOTPStatus,
  useStartTOTPEnrollment,
  useConfirmTOTPEnrollment,
  useCancelTOTPEnrollment,
} from "@/api/generated/inbok";
import type { TotpEnrollment } from "@/api/generated/model";
import type { ApiError } from "@/api/transport";
import { uncertainOutcome, userMessage } from "@/api/problem";
import { clearAccountCache } from "@/features/auth/session-cache";
import { RequestError } from "@/components/request-error";
import { Button } from "@/components/ui/button";
import { BackupCodes } from "./backup-codes";
import { ConfirmForm } from "./confirm-form";

const unauthorized = 401;
// QR codes require a clear border of four modules for scanners.
const qrQuietZone = 4;

// Enrollment sets up two-factor authentication while it is off, and shows
// the backup codes its confirmation returns.
export function Enrollment({
  locked,
  onWritingChange,
  onCodesChange,
}: {
  locked: boolean;
  onWritingChange: (pending: boolean) => void;
  onCodesChange: (present: boolean) => void;
}) {
  const client = useQueryClient();
  const navigate = useNavigate();
  const [enrollment, setEnrollment] = useState<TotpEnrollment>();
  const [codes, setCodes] = useState<string[]>();
  const [copyMessage, setCopyMessage] = useState("");
  const [notice, setNotice] = useState("");
  const startRef = useRef<HTMLButtonElement>(null);
  const cancelRef = useRef<HTMLButtonElement>(null);
  const checkRef = useRef<HTMLButtonElement>(null);
  const focusStart = useRef(false);
  const focusCancel = useRef(false);
  const setupRef = useRef<HTMLHeadingElement>(null);
  const setupFocused = useRef(false);
  const start = useStartTOTPEnrollment({
    mutation: {
      gcTime: 0,
      onMutate: prepareWrite,
      onSettled: () => {
        onWritingChange(false);
        refreshStatus();
      },
    },
  });
  const confirm = useConfirmTOTPEnrollment({
    mutation: {
      gcTime: 0,
      onMutate: prepareWrite,
      onSettled: () => {
        onWritingChange(false);
        refreshStatus();
        // Confirmation renews the session, ends the others, and may grant admin access.
        void client.invalidateQueries({ queryKey: getGetCurrentSessionQueryKey() });
        void client.invalidateQueries({ queryKey: getListSessionsQueryKey() });
      },
    },
  });
  const cancel = useCancelTOTPEnrollment({
    mutation: {
      gcTime: 0,
      onMutate: prepareWrite,
      onSettled: () => {
        onWritingChange(false);
        refreshStatus();
      },
    },
  });
  const pending = start.isPending || confirm.isPending || cancel.isPending;
  const status = useGetTOTPStatus({
    query: { enabled: !locked && !pending, gcTime: 0, networkMode: "always" },
  });
  const expired = [status.error, start.error, confirm.error, cancel.error].some(
    (error) => error?.status === unauthorized,
  );
  const confirmationUncertain = Boolean(confirm.error && uncertainOutcome(confirm.error));
  const uncertain =
    confirmationUncertain || Boolean(cancel.error && uncertainOutcome(cancel.error));
  const enabled = status.data?.data.status === "on";
  const blocked = pending || Boolean(codes);

  useEffect(() => () => onCodesChange(false), [onCodesChange]);

  useBlocker({
    shouldBlockFn: () => {
      if (!blocked || expired) return false;
      return !window.confirm(
        codes
          ? "These backup codes will not be shown again. Leave this page?"
          : "A two-factor authentication request is still running. Leave this page?",
      );
    },
    enableBeforeUnload: blocked && !expired,
  });

  useEffect(() => {
    if (!expired) return;
    clearAccountCache(client);
    void navigate({ to: "/sign-in", replace: true, ignoreBlocker: true });
  }, [expired, client, navigate]);

  useEffect(() => {
    if (!enrollment || locked || setupFocused.current) return;
    setupFocused.current = true;
    setupRef.current?.focus();
  }, [enrollment, locked]);

  useEffect(() => {
    if (!focusStart.current || locked || status.isFetching || !startRef.current) return;
    startRef.current.focus();
    focusStart.current = false;
  }, [notice, locked, status.isFetching]);

  useEffect(() => {
    if (!cancel.error || !focusCancel.current || pending || locked || status.isFetching) return;
    const target = uncertain ? checkRef.current : cancelRef.current;
    if (!target || target.disabled) return;
    target.focus();
    focusCancel.current = false;
  }, [cancel.error, pending, locked, uncertain, status.isFetching]);

  async function prepareWrite(): Promise<void> {
    onWritingChange(true);
    // An old-cookie read must not report expiry while confirmation renews it.
    await Promise.all([
      client.cancelQueries({ queryKey: getGetCurrentSessionQueryKey(), exact: true }),
      client.cancelQueries({ queryKey: getGetTOTPStatusQueryKey(), exact: true }),
      client.cancelQueries({ queryKey: getListSessionsQueryKey(), exact: true }),
    ]);
  }

  function refreshStatus(): void {
    void client.invalidateQueries({ queryKey: getGetTOTPStatusQueryKey() });
  }

  function startEnrollment(): void {
    if (locked || pending || status.isFetching || status.isError || enabled) return;
    start.mutate(undefined, {
      onSuccess: (result) => {
        setupFocused.current = false;
        setEnrollment(result.data);
        setCopyMessage("");
        setNotice("");
        cancel.reset();
        start.reset();
      },
    });
  }

  function confirmEnrollment(code: string, reject: (err: ApiError) => void): void {
    if (locked || pending || uncertain) return;
    cancel.reset();
    confirm.mutate(
      { data: { code } },
      {
        onSuccess: (result) => {
          onCodesChange(true);
          setCodes(result.data.backupCodes);
          setEnrollment(undefined);
          confirm.reset();
        },
        onError: reject,
      },
    );
  }

  function cancelEnrollment(): void {
    if (locked || pending || uncertain) return;
    focusCancel.current = true;
    cancel.mutate(undefined, {
      onSuccess: (result) => {
        // Use the committed result immediately so a cached pending state cannot flash.
        client.setQueryData(status.queryKey, result);
        setEnrollment(undefined);
        setCopyMessage("");
        focusStart.current = true;
        setNotice("Enrollment canceled.");
        start.reset();
        confirm.reset();
        cancel.reset();
      },
    });
  }

  async function copySecret(): Promise<void> {
    if (!enrollment) return;
    try {
      await navigator.clipboard.writeText(enrollment.secret);
      setCopyMessage("Setup key copied.");
    } catch {
      setCopyMessage("Could not copy. Select the setup key and copy it manually.");
    }
  }

  async function checkStatus(): Promise<void> {
    const result = await status.refetch();
    if (!result.isSuccess) return;
    cancel.reset();
    if (result.data.data.status === "on") return;
    // A confirmed pending state permits another deliberate attempt.
    confirm.reset();
    if (result.data.data.status === "off") {
      setEnrollment(undefined);
      setCopyMessage("");
      focusStart.current = true;
      setNotice("Enrollment canceled.");
    }
  }

  if (codes)
    return (
      <BackupCodes
        active={!locked}
        codes={codes}
        onDone={() => {
          setCodes(undefined);
          onCodesChange(false);
          refreshStatus();
        }}
      />
    );

  return (
    <section
      className="flex flex-col gap-6"
      aria-label="Two-factor authentication"
      aria-busy={pending}
    >
      {/* Stay mounted so screen readers announce changed text. */}
      <output className={notice ? undefined : "sr-only"}>{notice}</output>
      {status.isFetching && <output>Checking two-factor authentication...</output>}
      {status.error && (
        <div className="flex flex-col gap-3" role="alert">
          <p>Could not check two-factor authentication. {userMessage(status.error)}</p>
          <Button
            type="button"
            variant="outline"
            isDisabled={locked || status.isFetching}
            onPress={() => void status.refetch()}
          >
            Check status
          </Button>
        </div>
      )}
      {enabled ? null : enrollment ? (
        <>
          <h2 ref={setupRef} tabIndex={-1} className="text-xl font-semibold">
            Add inbok to your authenticator
          </h2>
          <p>Scan this QR code with your authenticator app, or enter the setup key manually.</p>
          <QRCodeSVG
            value={enrollment.uri}
            title="Scan to add inbok to your authenticator"
            size={224}
            marginSize={qrQuietZone}
            className="h-auto max-w-full"
          />
          <div className="flex flex-col gap-3">
            <p>Setup key</p>
            <code className="break-all select-all">{enrollment.secret}</code>
            <Button
              type="button"
              variant="outline"
              className="w-fit"
              isDisabled={locked || cancel.isPending}
              onPress={() => void copySecret()}
            >
              Copy setup key
            </Button>
            <output>{copyMessage}</output>
          </div>
          <p>
            Enter the six-digit code from the app to finish. Enabling two-factor authentication ends
            your other sessions.
          </p>
          <ConfirmForm
            purpose="enrollment"
            locked={uncertain || status.isError || cancel.isPending}
            checking={locked}
            pending={confirm.isPending}
            error={confirm.error}
            onEdit={() => {
              if (!uncertain) confirm.reset();
            }}
            onConfirm={confirmEnrollment}
          />
          {uncertain && (
            <Button
              ref={checkRef}
              type="button"
              variant="outline"
              isDisabled={locked || status.isFetching}
              onPress={() => void checkStatus()}
            >
              Check enrollment status
            </Button>
          )}
          <Button
            ref={cancelRef}
            type="button"
            variant="outline"
            isDisabled={locked || pending || uncertain}
            onPress={cancelEnrollment}
          >
            {cancel.isPending ? "Canceling enrollment..." : "Cancel enrollment"}
          </Button>
        </>
      ) : status.data && !status.isError ? (
        <>
          <p>Add an authenticator app to require a code when you sign in.</p>
          {status.data.data.status === "pending" && (
            <p>
              Enrollment was started but not confirmed. Starting again replaces the previous setup
              key; remove that entry from your authenticator app.
            </p>
          )}
          <Button
            ref={startRef}
            type="button"
            className="whitespace-normal"
            isDisabled={locked || pending || status.isFetching}
            onPress={startEnrollment}
          >
            {start.isPending
              ? "Starting enrollment..."
              : status.data.data.status === "pending"
                ? "Start enrollment again"
                : "Set up authenticator"}
          </Button>
        </>
      ) : null}
      {start.error && (
        <RequestError
          errors={[{ message: userMessage(start.error) }]}
          requestId={start.error.requestId}
        />
      )}
      {cancel.error && (
        <RequestError
          errors={[{ message: userMessage(cancel.error) }]}
          requestId={cancel.error.requestId}
        />
      )}
    </section>
  );
}
