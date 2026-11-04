import { useEffect, useRef, type ReactElement } from "react";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { resetPath } from "@/features/password-reset/path";
import { ResetForm } from "@/features/password-reset/reset-form";
import { useLinkSecret } from "@/hooks/use-link-secret";

const focusClass =
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid";
const linkClass = `inline-flex min-h-11 w-fit items-center underline underline-offset-4 ${focusClass}`;

export const Route = createFileRoute("/reset-password")({
  component: ResetPassword,
  head: () => ({ meta: [{ title: "Choose a new password - inbok" }] }),
});

function ResetPassword(): ReactElement {
  const navigate = useNavigate();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const secret = useLinkSecret(resetPath);

  useEffect(() => {
    headingRef.current?.focus();
  }, []);

  // signIn opens sign-in for email, the account just reset, in place of this
  // page.
  function signIn(email: string): void {
    // No session is created, so sign-in fills in the email and says why.
    // Replacing the entry keeps Back from returning to a spent link.
    void navigate({ to: "/sign-in", replace: true, state: { passwordResetEmail: email } });
  }

  return (
    <main className="mx-auto flex w-full max-w-lg flex-col gap-8 px-5 py-8 wrap-anywhere sm:px-8 sm:py-12">
      <h1 ref={headingRef} tabIndex={-1} className="text-3xl font-bold tracking-tight">
        Choose a new password
      </h1>
      {secret ? (
        <ResetForm key={secret} secret={secret} onDone={signIn} />
      ) : (
        <div className="flex flex-col gap-4">
          <p>Open this page from the link in your password reset email.</p>
          <Link to="/forgot-password" className={linkClass}>
            Ask for a new link
          </Link>
        </div>
      )}
    </main>
  );
}
