import type { ReactElement } from "react";
import { createFileRoute, Link } from "@tanstack/react-router";
import { ChevronLeftIcon } from "lucide-react";
import { useGetCurrentSession } from "@/api/generated/inbok";
import { cn } from "@/lib/cn";
import { lineLink } from "@/components/link-style";
import { SettingsPage } from "@/features/settings/page";
import { pageName } from "@/features/settings/text";
import { Enrollment } from "@/features/totp/enrollment";
import { twoFactorAbout, twoFactorTitle } from "@/features/totp/text";

export const Route = createFileRoute("/_app/settings/security/two-factor")({
  component: TwoFactor,
  head: () => ({ meta: [{ title: "Two-factor authentication - inbok" }] }),
});

// Nothing else on this page writes or ends the session, so the enrollment
// has no other write to wait for.
function ignore(): void {}

// TwoFactor is two-factor authentication's own page, under Security: the
// setup while it is off, or, while it is on, replacing the backup codes and
// turning it off. A task of several steps gets a page of its own rather than
// growing inside Security (DESIGN.md "Settings and signed-out pages").
function TwoFactor(): ReactElement {
  const { session } = Route.useRouteContext();
  // The live role: a refused turn-off rereads it when the member has become
  // an administrator since the page opened.
  const current = useGetCurrentSession({ query: { networkMode: "always" } });
  const role = current.data?.data.role ?? session.role;

  return (
    <SettingsPage
      title={twoFactorTitle}
      lead={twoFactorAbout}
      back={
        // Named for where it goes back to: the sidebar has a Security link too.
        <Link
          to="/settings/security"
          aria-label={`Back to ${pageName.security}`}
          className={cn(lineLink, "-mt-3 mb-1 gap-1 text-sm")}
        >
          <ChevronLeftIcon aria-hidden="true" className="size-4" />
          {pageName.security}
        </Link>
      }
    >
      <div className="py-6">
        <Enrollment role={role} locked={false} onWritingChange={ignore} onCodesChange={ignore} />
      </div>
    </SettingsPage>
  );
}
