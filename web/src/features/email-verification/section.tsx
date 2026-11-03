import { useEffect, useRef, type ReactElement } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { getGetCurrentSessionQueryKey, useRequestEmailVerification } from "@/api/generated/inbok";
import { clearAccountCache } from "@/features/auth/session-cache";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { alreadyVerified, sendErrorMessage, sentMessage } from "./text";

const unauthorized = 401;

// EmailVerificationSection shows the account's address and whether it is
// verified, and sends a verification link while it is not. locked disables
// sending; onWritingChange receives whether a send is running.
export function EmailVerificationSection({
  email,
  verified,
  locked,
  onWritingChange,
}: {
  email: string;
  verified: boolean;
  locked: boolean;
  onWritingChange: (pending: boolean) => void;
}): ReactElement {
  const client = useQueryClient();
  const navigate = useNavigate();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  // Set when a send starts; focus returns to the button once it ends.
  const refocus = useRef(false);
  const send = useRequestEmailVerification({
    mutation: {
      gcTime: 0,
      onMutate: () => onWritingChange(true),
      onSettled: (_, err) => {
        onWritingChange(false);
        // The address was verified elsewhere, e.g. from a link in another tab.
        if (err && alreadyVerified(err))
          void client.invalidateQueries({ queryKey: getGetCurrentSessionQueryKey() });
      },
    },
  });
  const expired = send.error?.status === unauthorized;
  const notice =
    send.data && !verified
      ? sentMessage(send.data.data.address)
      : send.error && alreadyVerified(send.error) && verified
        ? "Your email address is already verified."
        : "";

  useEffect(() => {
    if (!expired) return;
    clearAccountCache(client);
    void navigate({ to: "/sign-in", replace: true, ignoreBlocker: true });
  }, [expired, client, navigate]);

  // The button is disabled while sending or locked; give focus back once it
  // is usable, or to the heading once a verified address removed it.
  useEffect(() => {
    if (!refocus.current || send.isPending || locked) return;
    refocus.current = false;
    // Focus the user moved elsewhere in the meantime stays there.
    if (document.activeElement === document.body)
      (buttonRef.current ?? headingRef.current)?.focus();
  });

  return (
    <section className="flex flex-col gap-6" aria-labelledby="email-heading">
      <h2 id="email-heading" ref={headingRef} tabIndex={-1} className="text-xl font-semibold">
        Email
      </h2>
      <p className="flex flex-wrap items-center gap-2">
        <span className="font-medium">{email}</span>
        {verified ? (
          <Badge variant="secondary">Verified</Badge>
        ) : (
          <Badge variant="outline">Not verified</Badge>
        )}
      </p>
      {!verified && (
        <>
          <p>
            We&apos;ll email you a link. Open it to confirm this address is yours. Until you do, a
            forgotten password can&apos;t be reset by email.
          </p>
          <Button
            ref={buttonRef}
            type="button"
            variant="outline"
            className="h-auto min-h-11 w-fit border-muted-foreground whitespace-normal focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
            disabled={locked || send.isPending}
            onClick={() => {
              if (locked || send.isPending) return;
              refocus.current = true;
              send.mutate();
            }}
          >
            {send.isPending ? "Sending verification link..." : "Send verification link"}
          </Button>
          {send.error && !expired && (
            <div className="flex flex-col gap-2" role="alert">
              <p>{sendErrorMessage(send.error)}</p>
              {send.error.requestId && (
                <p className="text-sm text-muted-foreground">
                  Request reference: <code className="break-all">{send.error.requestId}</code>
                </p>
              )}
            </div>
          )}
        </>
      )}
      {/* Stay mounted so screen readers announce changed text. */}
      <output aria-atomic="true" className={notice ? undefined : "sr-only"}>
        {notice}
      </output>
    </section>
  );
}
