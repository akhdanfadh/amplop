import { Link } from "@tanstack/react-router";
import { cn } from "cn";
import type { CurrentSession } from "@/api/generated/model";

const linkClass =
  "inline-flex min-h-11 items-center underline underline-offset-4 focus-visible:focus-ring";

// AccountNav links the account's pages. account is the page's current
// session, absent while it loads; Members and Settings are shown only with
// admin access. wide matches a page whose content is wider than a form.
export function AccountNav({
  account,
  wide = false,
}: {
  account: CurrentSession | undefined;
  wide?: boolean;
}) {
  const adminAccess = account?.role === "admin" && !account.totpRequired;
  return (
    <header className={cn("mx-auto w-full px-5 pt-6 sm:px-8", wide ? "max-w-4xl" : "max-w-lg")}>
      <nav aria-label="Account" className="flex flex-wrap gap-6">
        <Link to="/" className={linkClass}>
          Home
        </Link>
        {/* Until the mail layout gives sending its own place. */}
        <Link to="/compose" className={linkClass}>
          New message
        </Link>
        <Link to="/outbox" className={linkClass}>
          Outbox
        </Link>
        <Link to="/security" className={linkClass}>
          Security
        </Link>
        <Link to="/connected-accounts" className={linkClass}>
          Connected accounts
        </Link>
        {adminAccess && (
          <>
            <Link to="/members" className={linkClass}>
              Members
            </Link>
            <Link to="/settings" className={linkClass}>
              Settings
            </Link>
          </>
        )}
      </nav>
    </header>
  );
}
