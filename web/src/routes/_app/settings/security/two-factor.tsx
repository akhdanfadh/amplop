import { useCallback, useEffect, useState, type ReactElement } from "react";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { ChevronLeftIcon } from "lucide-react";
import { useGetCurrentSession, useGetTOTPStatus } from "@/api/generated/inbok";
import type { ApiError } from "@/api/transport";
import { cn } from "@/lib/cn";
import { lineLink } from "@/components/link-style";
import { stepEnter } from "@/components/step-change";
import { clearAccountCache } from "@/features/auth/session-cache";
import { SettingsPage } from "@/features/settings/page";
import { pageName } from "@/features/settings/text";
import { BackupCodesSection, TurnOffSection, type OpenSection } from "@/features/totp/manage";
import { TwoFactorBadge } from "@/features/totp/security-section";
import { useSetupFlow, type SetupStage } from "@/features/totp/setup";
import {
  backupText,
  setUpName,
  setupLead,
  twoFactorAbout,
  twoFactorTitle,
} from "@/features/totp/text";

const unauthorized = 401;

export const Route = createFileRoute("/_app/settings/security/two-factor")({
  component: TwoFactor,
  head: () => ({ meta: [{ title: "Two-factor authentication - inbok" }] }),
});

// TwoFactor is two-factor authentication's own page, under Security: the
// setup while it is off, or, while it is on, replacing the backup codes and
// turning it off. A task of several steps gets a page of its own rather than
// growing inside Security (DESIGN.md "Settings and signed-out pages").
function TwoFactor(): ReactElement {
  const { session } = Route.useRouteContext();
  const client = useQueryClient();
  const navigate = useNavigate();
  const status = useGetTOTPStatus({ query: { networkMode: "always" } });
  // The live role: a refused turn-off rereads it, e.g. for a member made an
  // administrator since the page opened.
  const reader = useGetCurrentSession({ query: { networkMode: "always" } });
  const role = reader.data?.data.role ?? session.role;
  const current = status.data?.data;
  const on = current?.status === "on";
  // Whether this page has shown two-factor authentication on.
  const [shownOn, setShownOn] = useState(false);
  // Set while rendering, as React's "Adjusting some state when a prop
  // changes" does, so no render shows the setup in between.
  if (on && !shownOn) setShownOn(true);
  // A read that finds it off after that, e.g. a turn-off's own answer, one
  // whose answer was lost, or one from another tab, means it was turned off.
  const turnedOff = shownOn && current !== undefined && !on;
  // A 401 from any request on this page: the session ended, e.g. from
  // another browser, after the guard ran.
  const [ended, setEnded] = useState(false);
  const signedOut =
    ended || status.error?.status === unauthorized || reader.error?.status === unauthorized;
  const onError = useCallback((err: ApiError) => {
    if (err.status === unauthorized) setEnded(true);
  }, []);
  const back = (
    // Named for where it goes back to: the sidebar has a Security link too.
    <Link
      to="/settings/security"
      aria-label={`Back to ${pageName.security}`}
      className={cn(lineLink, "-mt-3 mb-1 gap-1 text-sm")}
    >
      <ChevronLeftIcon aria-hidden="true" className="size-4" />
      {pageName.security}
    </Link>
  );

  function backToSecurity(change: "on" | "off" | "canceled"): void {
    void navigate({
      to: "/settings/security",
      state: { twoFactorChange: change },
      ignoreBlocker: true,
    });
  }

  useEffect(() => {
    if (!signedOut) return;
    clearAccountCache(client);
    void navigate({ to: "/sign-in", replace: true, ignoreBlocker: true });
  }, [signedOut, client, navigate]);

  useEffect(() => {
    if (!turnedOff || signedOut) return;
    void navigate({
      to: "/settings/security",
      state: { twoFactorChange: "off" },
      ignoreBlocker: true,
    });
  }, [turnedOff, signedOut, navigate]);

  // Security takes over at once; the setup must not flash in between.
  if (turnedOff) return <></>;
  if (on)
    return (
      <Manage role={role} back={back} onError={onError} onStale={() => void status.refetch()} />
    );

  return (
    <Setup
      email={session.email}
      back={back}
      onError={onError}
      onFinished={() => backToSecurity("on")}
      onCanceled={() => backToSecurity("canceled")}
    />
  );
}

// Setup is the page while two-factor authentication is off: the setup's
// current step, which fades in as it changes (DESIGN.md "Motion"); the
// first step is there at once.
function Setup({
  email,
  back,
  onError,
  onFinished,
  onCanceled,
}: {
  email: string;
  back: ReactElement;
  onError: (err: ApiError) => void;
  onFinished: () => void;
  onCanceled: () => void;
}): ReactElement {
  const { stage, content } = useSetupFlow({ email, onFinished, onCanceled, onError });
  // Until the status is read, the page may yet be the one for managing it.
  const reading = stage === "loading" || stage === "error";
  // The first step shown after the read, and whether a later one has come.
  // Set while rendering, React's way to keep what an earlier render saw.
  const [first, setFirst] = useState<SetupStage>();
  const [stepped, setStepped] = useState(false);
  if (!reading && first === undefined) setFirst(stage);
  if (!reading && first !== undefined && !stepped && stage !== first) setStepped(true);
  return (
    <SettingsPage
      title={reading ? twoFactorTitle : setUpName}
      lead={reading ? twoFactorAbout : setupLead}
      back={back}
    >
      {/* Keyed by step, so each step enters afresh. */}
      <div key={stage} className={cn("py-6", stepped && stepEnter)}>
        {content}
      </div>
    </SettingsPage>
  );
}

// Manage is the page while two-factor authentication is on, with one
// section open at a time. Its notice reports replaced backup codes.
function Manage({
  role,
  back,
  onError,
  onStale,
}: {
  role: "member" | "admin";
  back: ReactElement;
  onError: (err: ApiError) => void;
  onStale: () => void;
}): ReactElement {
  const status = useGetTOTPStatus({ query: { networkMode: "always" } });
  const [notice, setNotice] = useState("");
  const [open, setOpen] = useState<OpenSection>("none");
  // Turning off is a member's; a role changed to administrator closes its
  // form, so the other section opens again.
  if (role !== "member" && open === "turnOff") setOpen("none");
  const current = status.data?.data;

  function openSection(next: OpenSection): void {
    setOpen(next);
    if (next !== "none") setNotice("");
  }

  return (
    <SettingsPage
      title={twoFactorTitle}
      aside={<TwoFactorBadge />}
      lead={twoFactorAbout}
      back={back}
      notice={notice}
    >
      {current && (
        <BackupCodesSection
          status={current}
          open={open}
          onOpen={openSection}
          onError={onError}
          onStale={onStale}
          onReplaced={() => setNotice(backupText.replaced)}
        />
      )}
      <TurnOffSection
        role={role}
        open={open}
        onOpen={openSection}
        onError={onError}
        onStale={onStale}
      />
    </SettingsPage>
  );
}
