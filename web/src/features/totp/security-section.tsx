import type { ReactElement } from "react";
import { Link } from "@tanstack/react-router";
import { ShieldCheckIcon } from "lucide-react";
import { useGetTOTPStatus } from "@/api/generated/amplop";
import { userMessage } from "@/api/problem";
import { cn } from "@/lib/cn";
import { ReadError } from "@/components/read-error";
import { StatusBadge } from "@/components/status-badge";
import { buttonVariants } from "@/components/ui/button";
import { isSessionEnded } from "@/features/auth/guard";
import { SettingsSection } from "@/features/settings/page";
import {
  checkingStatus,
  codesLeft,
  manageLink,
  offState,
  onBadge,
  setUpName,
  statusReadFailed,
  twoFactorAbout,
  twoFactorTitle,
} from "./text";

// TwoFactorBadge is the status badge for two-factor authentication on.
export function TwoFactorBadge(): ReactElement {
  return (
    <StatusBadge tone="success" icon={ShieldCheckIcon}>
      {onBadge}
    </StatusBadge>
  );
}

// TwoFactorSection is Security's two-factor row: on, with the backup codes
// left and a way to manage it, or off, with a way to set it up. An
// unfinished setup counts as off: setting up continues it. The setup and
// the changes happen on the two-factor page.
export function TwoFactorSection(): ReactElement {
  const status = useGetTOTPStatus({ query: { networkMode: "always" } });
  const current = status.data?.data;
  const on = current?.status === "on";
  // An ended session, which the session-end rule answers (watchSessionEnd).
  const ended = isSessionEnded(status.error);

  return (
    <SettingsSection
      title={twoFactorTitle}
      about={twoFactorAbout}
      aside={on ? <TwoFactorBadge /> : undefined}
    >
      {/* Only a first read says so: background re-reads show nothing. */}
      {!current && status.isFetching && <output>{checkingStatus}</output>}
      {/* A failed refresh keeps the last state shown and says it may be old. */}
      {status.error && !ended && (
        <ReadError
          message={`${statusReadFailed} ${userMessage(status.error)}`}
          error={status.error}
          retrying={status.isFetching}
          onRetry={() => void status.refetch()}
        />
      )}
      {current && on && (
        <>
          <p>{codesLeft(current)}</p>
          <Link
            to="/settings/security/two-factor"
            className={cn(buttonVariants({ variant: "outline" }), "whitespace-normal")}
          >
            {manageLink}
          </Link>
        </>
      )}
      {current && !on && (
        <>
          <p>{offState}</p>
          <Link
            to="/settings/security/two-factor"
            className={cn(buttonVariants({ variant: "default" }), "whitespace-normal")}
          >
            {setUpName}
          </Link>
        </>
      )}
    </SettingsSection>
  );
}
