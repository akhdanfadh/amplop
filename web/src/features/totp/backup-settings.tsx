import { useEffect, useRef, useState } from "react";
import { totpStatusBackupCodesLeftMax } from "@/api/generated/model";
import type { ApiError } from "@/api/transport";
import { uncertainSession } from "@/features/auth/session-error";
import { Button } from "@/components/ui/button";
import { ConfirmForm } from "./confirm-form";

// Prompt replacement before the last recovery codes are exhausted.
const lowBackupCodes = 2;

export function BackupSettings({
  left,
  locked,
  pending,
  checking,
  error,
  onEdit,
  onReplace,
  onCheck,
}: {
  left: number;
  locked: boolean;
  pending: boolean;
  checking: boolean;
  error: ApiError | null;
  onEdit: () => void;
  onReplace: (code: string, reject: (err: ApiError) => void) => void;
  onCheck: () => void;
}) {
  const [replacing, setReplacing] = useState(false);
  const replaceRef = useRef<HTMLButtonElement>(null);
  const checkRef = useRef<HTMLButtonElement>(null);
  const restoreFocus = useRef(false);
  const focusedError = useRef<ApiError | null>(null);
  const uncertain = Boolean(error && uncertainSession(error));

  useEffect(() => {
    if (replacing || !restoreFocus.current || locked) return;
    replaceRef.current?.focus();
    restoreFocus.current = false;
  }, [replacing, locked]);

  useEffect(() => {
    if (!uncertain || focusedError.current === error || locked || checking) return;
    checkRef.current?.focus();
    focusedError.current = error;
  }, [error, uncertain, locked, checking]);

  return (
    <section className="flex flex-col gap-6" aria-labelledby="backup-settings-heading">
      <h2 id="backup-settings-heading" className="text-xl font-semibold">
        Backup codes
      </h2>
      <p>
        {left} of {totpStatusBackupCodesLeftMax} backup codes remain.
      </p>
      {left <= lowBackupCodes && (
        <p>
          {left === 0
            ? "No backup codes remain. Generate a new set while you can access your authenticator."
            : "You are running low on backup codes. Generate a new set before they run out."}
        </p>
      )}
      {replacing ? (
        <>
          <p>
            Generating a new set invalidates every old backup code. Enter a fresh code from your
            authenticator app. Backup codes cannot authorize replacement.
          </p>
          <ConfirmForm
            purpose="regeneration"
            locked={locked || checking || uncertain}
            pending={pending}
            error={error}
            onEdit={onEdit}
            onConfirm={onReplace}
          />
          {uncertain && (
            <>
              <p>
                Your old backup codes may no longer work. Check status, then use a fresh
                authenticator code to generate another set.
              </p>
              <Button
                ref={checkRef}
                type="button"
                variant="outline"
                className="min-h-11 border-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
                disabled={locked || checking}
                onClick={onCheck}
              >
                {checking ? "Checking backup-code status..." : "Check backup-code status"}
              </Button>
            </>
          )}
          <Button
            type="button"
            variant="outline"
            className="min-h-11 border-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
            disabled={locked || pending || checking || uncertain}
            onClick={() => {
              onEdit();
              restoreFocus.current = true;
              setReplacing(false);
            }}
          >
            Cancel replacement
          </Button>
        </>
      ) : (
        <Button
          ref={replaceRef}
          type="button"
          variant="outline"
          className="min-h-11 border-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
          disabled={locked || checking}
          onClick={() => setReplacing(true)}
        >
          Replace backup codes
        </Button>
      )}
    </section>
  );
}
