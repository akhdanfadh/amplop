import { useEffect, useRef, useState } from "react";
import type { ApiError } from "@/api/transport";
import { uncertainSession } from "@/features/auth/session-error";
import { Button } from "@/components/ui/button";
import { ConfirmForm } from "./confirm-form";

export function TurnOff({
  locked,
  pending,
  checking,
  error,
  onEdit,
  onTurnOff,
  onCheck,
}: {
  locked: boolean;
  pending: boolean;
  checking: boolean;
  error: ApiError | null;
  onEdit: () => void;
  onTurnOff: (code: string, reject: (err: ApiError) => void) => void;
  onCheck: () => void;
}) {
  const [confirming, setConfirming] = useState(false);
  const openRef = useRef<HTMLButtonElement>(null);
  const checkRef = useRef<HTMLButtonElement>(null);
  const restoreFocus = useRef(false);
  const focusedError = useRef<ApiError | null>(null);
  const uncertain = Boolean(error && uncertainSession(error));

  useEffect(() => {
    if (confirming || !restoreFocus.current || locked) return;
    openRef.current?.focus();
    restoreFocus.current = false;
  }, [confirming, locked]);

  useEffect(() => {
    if (!uncertain || focusedError.current === error || locked || checking) return;
    checkRef.current?.focus();
    focusedError.current = error;
  }, [error, uncertain, locked, checking]);

  return (
    <section className="flex flex-col gap-6" aria-labelledby="turn-off-heading">
      <h2 id="turn-off-heading" className="text-xl font-semibold">
        Turn off two-factor authentication
      </h2>
      {confirming ? (
        <>
          <p>
            Turning off removes your authenticator and every backup code. Signing in will need only
            your password. Your sessions stay signed in. Enter a fresh authenticator code or an
            unused backup code.
          </p>
          <ConfirmForm
            purpose="turnOff"
            locked={locked || checking || uncertain}
            pending={pending}
            error={error}
            onEdit={onEdit}
            onConfirm={onTurnOff}
          />
          {uncertain && (
            <>
              <p>Two-factor authentication may already be off. Check status before trying again.</p>
              <Button
                ref={checkRef}
                type="button"
                variant="outline"
                className="min-h-11 border-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
                disabled={locked || checking}
                onClick={onCheck}
              >
                {checking ? "Checking two-factor status..." : "Check two-factor status"}
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
              setConfirming(false);
            }}
          >
            Keep two-factor authentication
          </Button>
        </>
      ) : (
        <Button
          ref={openRef}
          type="button"
          variant="outline"
          className="h-auto min-h-11 w-fit border-muted-foreground whitespace-normal focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
          disabled={locked || checking}
          onClick={() => setConfirming(true)}
        >
          Turn off two-factor authentication
        </Button>
      )}
    </section>
  );
}
