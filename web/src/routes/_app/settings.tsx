import type { ReactElement } from "react";
import { Link, Outlet, createFileRoute, useLocation } from "@tanstack/react-router";
import { Frame } from "@/components/frame";
import { useSignedInSession } from "@/features/auth/session";
import { SettingsNav } from "@/features/settings/nav";
import { unverifiedNotice, verifyLink } from "@/features/settings/text";

const linkClass = "underline underline-offset-4 hover:decoration-2 focus-visible:focus-ring";

// The settings frame: the settings sidebar beside one settings page.
export const Route = createFileRoute("/_app/settings")({
  // NOTE: only the loader, which this route lacks, is split off, so the
  // frame comes with the app, and a page still loading as the member moves
  // between pages shows its loading view inside the frame, not over the
  // whole window; the first load is one loading view over the window.
  codeSplitGroupings: [["loader"]],
  component: SettingsFrame,
});

function SettingsFrame(): ReactElement {
  return (
    <Frame label="Settings" nav={<SettingsNav />}>
      <UnverifiedBanner />
      <Outlet />
    </Frame>
  );
}

// UnverifiedBanner asks a member whose email address isn't verified to
// verify it, on every settings page but Security, where they do it.
function UnverifiedBanner(): ReactElement | null {
  const session = useSignedInSession();
  const onSecurity = useLocation({
    select: (location) => location.pathname.startsWith("/settings/security"),
  });
  if (!session || session.emailVerified || onSecurity) return null;

  return (
    // Password reset emails only a verified address, and its answer cannot
    // say so without telling which accounts exist.
    <p className="bg-info/8 px-4 py-2 text-sm text-info dark:bg-info/12">
      {unverifiedNotice}{" "}
      <Link to="/settings/security" className={linkClass}>
        {verifyLink}
      </Link>
      .
    </p>
  );
}
