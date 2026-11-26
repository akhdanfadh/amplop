import type { ReactElement } from "react";
import { Link, Outlet, createFileRoute } from "@tanstack/react-router";
import { OfflineBanner } from "@/components/offline-banner";

// The signed-out layout (DESIGN.md "Settings and signed-out pages"): the
// wordmark over one column centred both ways on the muted surface, for
// sign-in, setup, registration, password reset, and email verification.
// Each page puts its content in a SignedOutPage.
export const Route = createFileRoute("/_auth")({ component: AuthLayout });

function AuthLayout(): ReactElement {
  return (
    <div className="flex min-h-svh flex-col bg-muted">
      <OfflineBanner />
      {/* A container, so content laid out by its width, e.g. the setup's
          phone link, measures the window here as on a settings page. */}
      <div className="@container flex flex-1 flex-col items-center justify-center px-4 py-10 sm:px-6">
        <div className="flex w-full max-w-sm flex-col gap-6">
          <Link
            to="/"
            className="-mx-1.5 self-center rounded-md px-1.5 text-lg font-semibold outline-none focus-visible:focus-ring"
          >
            amplop
          </Link>
          <Outlet />
        </div>
      </div>
    </div>
  );
}
