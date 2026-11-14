import { useEffect, useRef, useState } from "react";
import { totpStatusBackupCodesLeftMax } from "@/api/generated/model";
import type { ApiError } from "@/api/transport";
import { uncertainOutcome } from "@/api/problem";
import { Button } from "@/components/ui/button";
import { ConfirmForm } from "./confirm-form";

// Prompt replacement before the last recovery codes are exhausted.
export const lowBackupCodes = 2;

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
  const uncertain = Boolean(error && uncertainOutcome(error));

  useEffect(() => {
    if (replacing || !restoreFocus.current || locked || checking) return;
    replaceRef.current?.focus();
    restoreFocus.current = false;
  }, [replacing, locked, checking]);

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
            locked={locked || uncertain}
            checking={checking}
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
                isDisabled={locked || checking}
                onPress={onCheck}
              >
                {checking ? "Checking backup-code status..." : "Check backup-code status"}
              </Button>
            </>
          )}
          <Button
            type="button"
            variant="outline"
            isDisabled={locked || pending || checking || uncertain}
            onPress={() => {
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
          isDisabled={locked || checking}
          onPress={() => setReplacing(true)}
        >
          Replace backup codes
        </Button>
      )}
    </section>
  );
}
