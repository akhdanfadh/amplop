import type { ReactElement } from "react";
import { Link, Outlet, createFileRoute } from "@tanstack/react-router";
import { OfflineBanner } from "@/components/offline-banner";

// The signed-out layout (DESIGN.md "Settings and signed-out pages"): the
// wordmark over one centred column, for sign-in, setup, registration,
// password reset, and email verification.
export const Route = createFileRoute("/_auth")({ component: AuthLayout });

function AuthLayout(): ReactElement {
  return (
    <div className="flex min-h-svh flex-col">
      <OfflineBanner />
      <div className="mx-auto w-full max-w-md">
        <div className="px-5 pt-8 sm:px-8 sm:pt-12">
          <Link
            to="/"
            className="rounded-md text-lg font-semibold outline-none focus-visible:focus-ring"
          >
            inbok
          </Link>
        </div>
        <Outlet />
      </div>
    </div>
  );
}
