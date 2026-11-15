import { useEffect, type ReactElement } from "react";
import { Link, useMatchRoute } from "@tanstack/react-router";
import { MailWarningIcon, ShieldAlertIcon, type LucideIcon } from "lucide-react";
import { useGetCurrentSession, useGetSystemEmail } from "@/api/generated/inbok";
import { Banner } from "@/components/banner";
import { textLink } from "@/components/link-style";
import { loadedFreshMs, useSessionEnd } from "@/features/auth/session-end";
import { systemEmailState } from "@/features/system-email/state";
import { reminderText, remindersLabel } from "./text";

// A setup step the signed-in member can still owe.
export type Step = "setUpSystemEmail" | "testSystemEmail" | "verifyEmail";

// Where each step is done, and its icon.
const steps = {
  setUpSystemEmail: { icon: MailWarningIcon, to: "/settings/system-email" },
  testSystemEmail: { icon: MailWarningIcon, to: "/settings/system-email" },
  verifyEmail: { icon: ShieldAlertIcon, to: "/settings/security" },
} as const satisfies Record<Step, { icon: LucideIcon; to: string }>;

// SetupReminder shows a banner for each setup step the signed-in member
// owes, stacked in a landmark named for them: an administrator's system
// email, while nothing is saved or the saved settings passed no test, then
// the member's own unverified address. It shows nothing once every step is
// done. System email is left out until its first read answers, and when
// that read fails; a failed later read keeps the last answer, as the System
// email page does.
//
// DESIGN: each banner follows its own fact from the server and none waits
// for another; what a step cannot do yet, e.g. a verification link while
// nothing is saved, the server refuses and says why.
export function SetupReminder(): ReactElement | null {
  const sessionEnd = useSessionEnd();
  // Reread when the window regains focus, unlike the frame's other session
  // readers: the address is often verified from the email in another tab.
  const sessionRead = useGetCurrentSession({
    query: { refetchOnMount: false, refetchOnReconnect: false },
  });
  const session = sessionRead.data?.data;
  const admin = session?.role === "admin";
  // The System email page's own read: its saves and tests mark it out of
  // date, so the reminder follows them.
  const systemEmail = useGetSystemEmail({ query: { enabled: admin, staleTime: loadedFreshMs } });

  // Either read can find the session ended, e.g. the focus reread on a page
  // whose own reads are still fresh.
  useEffect(() => {
    if (!sessionEnd(sessionRead.error)) sessionEnd(systemEmail.error);
  }, [sessionRead.error, systemEmail.error, sessionEnd]);

  if (!session) return null;

  const state =
    admin && systemEmail.data ? systemEmailState(systemEmail.data.data.settings) : undefined;
  const owed: Step[] = [];
  if (state === "none") owed.push("setUpSystemEmail");
  if (state === "untested") owed.push("testSystemEmail");
  if (!session.emailVerified) owed.push("verifyEmail");
  if (owed.length === 0) return null;

  return (
    // Before the page's heading, which takes focus, and outside its main: a
    // landmark lets a screen reader find them.
    <section aria-label={remindersLabel} className="flex shrink-0 flex-col">
      {owed.map((step) => (
        <StepBanner key={step} step={step} />
      ))}
    </section>
  );
}

// StepBanner is the banner for step: what is not done and what that stops,
// then a link to where it is done, except on that page.
function StepBanner({ step }: { step: Step }): ReactElement {
  const { icon, to } = steps[step];
  const here = useMatchRoute()({ to });

  return (
    <Banner tone="info" icon={icon}>
      {reminderText[step].notice}
      {/* A link to the page it is on would do nothing. */}
      {!here && (
        <>
          {" "}
          <Link to={to} className={textLink}>
            {reminderText[step].link}
          </Link>
          .
        </>
      )}
    </Banner>
  );
}
