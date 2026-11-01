import { Link } from "@tanstack/react-router";
import type { CurrentSession } from "@/api/generated/model";

const linkClass =
  "inline-flex min-h-11 items-center underline underline-offset-4 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid";

// AccountNav links the account's pages. account is the page's current
// session, absent while it loads; Settings is shown only with admin access.
export function AccountNav({ account }: { account: CurrentSession | undefined }) {
  return (
    <header className="mx-auto w-full max-w-lg px-5 pt-6 sm:px-8">
      <nav aria-label="Account" className="flex flex-wrap gap-6">
        <Link to="/" className={linkClass}>
          Home
        </Link>
        <Link to="/security" className={linkClass}>
          Security
        </Link>
        {account?.role === "admin" && !account.totpRequired && (
          <Link to="/settings" className={linkClass}>
            Settings
          </Link>
        )}
      </nav>
    </header>
  );
}
