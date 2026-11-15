import { useEffect, useRef, type ReactElement } from "react";
import { CircleCheckIcon, CircleDashedIcon } from "lucide-react";
import { useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { getGetCurrentSessionQueryKey, useRequestEmailVerification } from "@/api/generated/inbok";
import { clearAccountCache } from "@/features/auth/session-cache";
import { StatusBadge } from "@/components/status-badge";
import { SettingsSection } from "@/features/settings/page";
import { Button } from "@/components/ui/button";
import { alreadyVerified, emailAbout, emailTitle, sendErrorMessage, sentMessage } from "./text";

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
  // Set when a send starts; once it ends, focus that fell to the page goes
  // back to the button, or to the heading if the answer removed it.
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

  // The button keeps focus while it sends. Focus falls to the page when a
  // lock disables it meanwhile, or a verified address removes it: give it
  // back to the button once usable, or to the heading.
  useEffect(() => {
    if (!refocus.current || send.isPending || locked) return;
    refocus.current = false;
    // Focus the user moved elsewhere in the meantime stays there.
    if (document.activeElement === document.body)
      (buttonRef.current ?? headingRef.current)?.focus();
  });

  return (
    <SettingsSection title={emailTitle} about={emailAbout} headingRef={headingRef}>
      <p className="flex flex-wrap items-center gap-2">
        <span className="font-medium">{email}</span>
        {verified ? (
          <StatusBadge tone="success" icon={CircleCheckIcon}>
            Verified
          </StatusBadge>
        ) : (
          <StatusBadge tone="neutral" icon={CircleDashedIcon}>
            Not verified
          </StatusBadge>
        )}
      </p>
      {!verified && (
        <>
          <p className="text-sm text-muted-foreground">
            We&apos;ll email you a link. Open it to confirm this address is yours. Until you do, a
            forgotten password can&apos;t be reset by email.
          </p>
          <Button
            ref={buttonRef}
            type="button"
            variant="outline"
            className="whitespace-normal"
            // Pending, not disabled: focus stays on the button while it
            // sends, and presses do nothing.
            isDisabled={locked}
            isPending={send.isPending}
            onPress={() => {
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
    </SettingsSection>
  );
}
