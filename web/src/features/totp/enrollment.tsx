import { useEffect, useRef, useState } from "react";
import { useBlocker, useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { QRCodeSVG } from "qrcode.react";
import {
  getGetCurrentSessionQueryKey,
  getGetTOTPStatusQueryKey,
  useGetTOTPStatus,
  useStartTOTPEnrollment,
  useConfirmTOTPEnrollment,
  useCancelTOTPEnrollment,
  useRegenerateTOTPBackupCodes,
  useDisableTOTP,
} from "@/api/generated/inbok";
import type { CurrentSession, TotpEnrollment } from "@/api/generated/model";
import type { ApiError } from "@/api/transport";
import { userMessage } from "@/api/problem";
import { uncertainSession } from "@/features/auth/session-error";
import { clearAccountCache } from "@/features/auth/session-cache";
import { Button } from "@/components/ui/button";
import { FieldError } from "@/components/ui/field";
import { BackupCodes } from "./backup-codes";
import { ConfirmForm } from "./confirm-form";
import { BackupSettings } from "./backup-settings";
import { TurnOff } from "./turn-off";

const unauthorized = 401;
const forbidden = 403;
const conflict = 409;
const turnedOffNotice =
  "Two-factor authentication is off. Remove inbok from your authenticator app.";
// QR codes require a clear border of four modules for scanners.
const qrQuietZone = 4;

export function Enrollment({
  role,
  locked,
  onWritingChange,
  onCodesChange,
}: {
  role: CurrentSession["role"];
  locked: boolean;
  onWritingChange: (pending: boolean) => void;
  onCodesChange: (present: boolean) => void;
}) {
  const client = useQueryClient();
  const navigate = useNavigate();
  const [enrollment, setEnrollment] = useState<TotpEnrollment>();
  const [codes, setCodes] = useState<string[]>();
  const [codeSource, setCodeSource] = useState<"enrollment" | "regeneration">("enrollment");
  const [copyMessage, setCopyMessage] = useState("");
  const [notice, setNotice] = useState("");
  const startRef = useRef<HTMLButtonElement>(null);
  const cancelRef = useRef<HTMLButtonElement>(null);
  const checkRef = useRef<HTMLButtonElement>(null);
  const focusStart = useRef(false);
  const adminRef = useRef<HTMLParagraphElement>(null);
  const focusAdmin = useRef(false);
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
        // Confirmation renews the session and may grant admin access.
        void client.invalidateQueries({ queryKey: getGetCurrentSessionQueryKey() });
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
  const regenerate = useRegenerateTOTPBackupCodes({
    mutation: {
      gcTime: 0,
      onMutate: prepareWrite,
      onSettled: () => {
        onWritingChange(false);
        refreshStatus();
      },
    },
  });
  const disable = useDisableTOTP({
    mutation: {
      gcTime: 0,
      onMutate: prepareWrite,
      onSettled: () => {
        onWritingChange(false);
        refreshStatus();
      },
    },
  });
  const pending =
    start.isPending ||
    confirm.isPending ||
    cancel.isPending ||
    regenerate.isPending ||
    disable.isPending;
  const status = useGetTOTPStatus({
    query: { enabled: !locked && !pending, gcTime: 0, networkMode: "always" },
  });
  const expired = [
    status.error,
    start.error,
    confirm.error,
    cancel.error,
    regenerate.error,
    disable.error,
  ].some((error) => error?.status === unauthorized);
  const confirmationUncertain = Boolean(confirm.error && uncertainSession(confirm.error));
  const regenerationUncertain = Boolean(regenerate.error && uncertainSession(regenerate.error));
  const turnOffUncertain = Boolean(disable.error && uncertainSession(disable.error));
  const resetTurnOff = disable.reset;
  const uncertain =
    confirmationUncertain ||
    regenerationUncertain ||
    Boolean(cancel.error && uncertainSession(cancel.error));
  const factor = status.data?.data;
  // A status read can show that a turn-off with a lost response committed.
  const turnedOff = turnOffUncertain && factor?.status === "off";
  const shownNotice = turnedOff ? turnedOffNotice : notice;
  const enabled = factor?.status === "on";
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
    if (turnedOff) focusStart.current = true;
  }, [turnedOff]);

  useEffect(() => {
    if (!focusStart.current || locked || status.isFetching || !startRef.current) return;
    startRef.current.focus();
    focusStart.current = false;
  }, [shownNotice, locked, status.isFetching]);

  useEffect(() => {
    // Administrators cannot turn off; drop a stale attempt and its code.
    if (role !== "member") resetTurnOff();
  }, [role, resetTurnOff]);

  useEffect(() => {
    if (role === "member" || !focusAdmin.current || locked) return;
    adminRef.current?.focus();
    focusAdmin.current = false;
  }, [role, locked]);

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
        regenerate.reset();
        disable.reset();
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
          setCodeSource("enrollment");
          setCodes(result.data.backupCodes);
          setEnrollment(undefined);
          confirm.reset();
        },
        onError: reject,
      },
    );
  }

  function replaceCodes(code: string, reject: (err: ApiError) => void): void {
    if (
      locked ||
      pending ||
      regenerationUncertain ||
      status.isFetching ||
      status.isError ||
      !enabled
    )
      return;
    regenerate.mutate(
      { data: { code } },
      {
        onSuccess: (result) => {
          setNotice("");
          onCodesChange(true);
          setCodeSource("regeneration");
          setCodes(result.data.backupCodes);
          setEnrollment(undefined);
          confirm.reset();
          regenerate.reset();
        },
        onError: reject,
      },
    );
  }

  function turnOff(code: string, reject: (err: ApiError) => void): void {
    if (
      locked ||
      pending ||
      turnOffUncertain ||
      status.isFetching ||
      status.isError ||
      !enabled ||
      role !== "member"
    )
      return;
    setNotice("");
    disable.mutate(
      { data: { code } },
      {
        onSuccess: (result) => {
          client.setQueryData(status.queryKey, result);
          focusStart.current = true;
          setNotice(turnedOffNotice);
          confirm.reset();
          regenerate.reset();
          disable.reset();
        },
        onError: (err) => {
          if (err.status === forbidden) {
            // The role may have changed since this page read the session.
            focusAdmin.current = true;
            void client.invalidateQueries({ queryKey: getGetCurrentSessionQueryKey() });
          }
          if (err.status === conflict) {
            disable.reset();
            // Announce only what a fresh status read confirms.
            void status.refetch().then((result) => {
              if (!result.isSuccess || result.data.data.status === "on") return;
              focusStart.current = true;
              setNotice("Two-factor authentication is already off.");
            });
            return;
          }
          reject(err);
        },
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
    disable.reset();
    if (regenerationUncertain) {
      // A count cannot recover a lost code set, even when it says ten remain.
      regenerate.reset();
      setNotice(
        "Replacement codes could not be retrieved. Your old codes may no longer work. Generate a new set with a fresh authenticator code.",
      );
    }
    if (result.data.data.status === "on") {
      if (turnOffUncertain)
        setNotice("Two-factor authentication is still on. Try again with a new code.");
      return;
    }
    // A confirmed pending state permits another deliberate attempt.
    confirm.reset();
    if (result.data.data.status === "off") {
      setEnrollment(undefined);
      setCopyMessage("");
      focusStart.current = true;
      setNotice(
        turnOffUncertain
          ? turnedOffNotice
          : regenerationUncertain
            ? "Two-factor authentication is no longer enabled."
            : "Enrollment canceled.",
      );
    }
  }

  if (codes)
    return (
      <BackupCodes
        active={!locked}
        codes={codes}
        source={codeSource}
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
      <output className={shownNotice ? undefined : "sr-only"}>{shownNotice}</output>
      {status.isFetching && <output>Checking two-factor authentication...</output>}
      {status.error && (
        <div className="flex flex-col gap-3" role="alert">
          <p>Could not check two-factor authentication. {userMessage(status.error)}</p>
          <Button
            type="button"
            variant="outline"
            className="min-h-11 border-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
            disabled={locked || status.isFetching}
            onClick={() => {
              if (regenerationUncertain || turnOffUncertain) void checkStatus();
              else void status.refetch();
            }}
          >
            Check status
          </Button>
        </div>
      )}
      {enabled ? (
        <>
          <p>Two-factor authentication is enabled.</p>
          {confirmationUncertain && (
            <p>We could not retrieve your backup codes. Keep access to your authenticator app.</p>
          )}
          <BackupSettings
            left={factor.backupCodesLeft}
            locked={locked || pending || status.isError || (turnOffUncertain && role === "member")}
            pending={regenerate.isPending}
            checking={status.isFetching}
            error={regenerate.error}
            onEdit={() => {
              if (!regenerationUncertain) regenerate.reset();
            }}
            onReplace={replaceCodes}
            onCheck={() => void checkStatus()}
          />
          {role === "member" ? (
            <TurnOff
              locked={locked || pending || status.isError || regenerationUncertain}
              pending={disable.isPending}
              checking={status.isFetching}
              error={disable.error}
              onEdit={() => {
                if (!turnOffUncertain) disable.reset();
              }}
              onTurnOff={turnOff}
              onCheck={() => void checkStatus()}
            />
          ) : (
            <p ref={adminRef} tabIndex={-1}>
              Administrators must keep two-factor authentication enabled.
            </p>
          )}
        </>
      ) : enrollment ? (
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
              className="min-h-11 w-fit border-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
              disabled={locked || cancel.isPending}
              onClick={() => void copySecret()}
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
            locked={locked || uncertain || status.isError || cancel.isPending}
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
              className="min-h-11 border-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
              disabled={locked || status.isFetching}
              onClick={() => void checkStatus()}
            >
              Check enrollment status
            </Button>
          )}
          <Button
            ref={cancelRef}
            type="button"
            variant="outline"
            className="min-h-11 border-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
            disabled={locked || pending || uncertain}
            onClick={cancelEnrollment}
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
            className="h-auto min-h-11 whitespace-normal focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
            disabled={locked || pending || status.isFetching}
            onClick={startEnrollment}
          >
            {start.isPending
              ? "Starting enrollment..."
              : status.data.data.status === "pending"
                ? "Start enrollment again"
                : "Set up authenticator"}
          </Button>
        </>
      ) : null}
      {start.error && <FieldError errors={[{ message: userMessage(start.error) }]} />}
      {cancel.error && <FieldError errors={[{ message: userMessage(cancel.error) }]} />}
    </section>
  );
}
