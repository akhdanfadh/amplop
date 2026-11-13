import { useEffect, useRef, type ReactElement } from "react";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { CompleteForm } from "@/features/registration/complete-form";
import { completePath } from "@/features/registration/path";
import { useLinkSecret } from "@/hooks/use-link-secret";
import { lineLink } from "@/components/link-style";

export const Route = createFileRoute("/_auth/register/complete")({
  component: CompleteRegistration,
  head: () => ({ meta: [{ title: "Create your account - inbok" }] }),
});

function CompleteRegistration(): ReactElement {
  const navigate = useNavigate();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const secret = useLinkSecret(completePath);

  useEffect(() => {
    headingRef.current?.focus();
  }, []);

  // No session is created: sign-in fills in the email and says why.
  function signIn(email: string): void {
    void navigate({ to: "/sign-in", replace: true, state: { registeredEmail: email } });
  }

  return (
    <main className="mx-auto flex w-full max-w-lg flex-col gap-8 px-5 py-8 wrap-anywhere sm:px-8 sm:py-12">
      <h1 ref={headingRef} tabIndex={-1} className="text-3xl font-bold tracking-tight">
        Create your account
      </h1>
      {secret ? (
        <CompleteForm key={secret} secret={secret} onDone={signIn} />
      ) : (
        <div className="flex flex-col gap-4">
          <p>Open this page from the link in your sign-up email.</p>
          {/* Exact, or the router marks /register current here and a screen
              reader says so. */}
          <Link to="/register" activeOptions={{ exact: true }} className={lineLink}>
            Ask for a new link
          </Link>
        </div>
      )}
    </main>
  );
}
