import { Link } from "@tanstack/react-router";

export function AccountNav() {
  return (
    <header className="mx-auto w-full max-w-lg px-5 pt-6 sm:px-8">
      <nav aria-label="Account" className="flex flex-wrap gap-6">
        <Link
          to="/"
          className="inline-flex min-h-11 items-center underline underline-offset-4 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
        >
          Home
        </Link>
        <Link
          to="/security"
          className="inline-flex min-h-11 items-center underline underline-offset-4 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
        >
          Security
        </Link>
      </nav>
    </header>
  );
}
