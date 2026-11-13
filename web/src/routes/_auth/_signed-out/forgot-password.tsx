import { useEffect, useRef, type ReactElement } from "react";
import { createFileRoute, Link } from "@tanstack/react-router";
import { RequestForm } from "@/features/password-reset/request-form";
import { lineLink } from "@/components/link-style";

export const Route = createFileRoute("/_auth/_signed-out/forgot-password")({
  component: ForgotPassword,
  head: () => ({ meta: [{ title: "Reset your password - inbok" }] }),
});

function ForgotPassword(): ReactElement {
  const headingRef = useRef<HTMLHeadingElement>(null);

  useEffect(() => {
    headingRef.current?.focus();
  }, []);

  return (
    <main className="mx-auto flex w-full max-w-lg flex-col gap-8 px-5 py-8 wrap-anywhere sm:px-8 sm:py-12">
      <h1 ref={headingRef} tabIndex={-1} className="text-3xl font-bold tracking-tight">
        Reset your password
      </h1>
      <RequestForm />
      <Link to="/sign-in" className={lineLink}>
        Back to sign in
      </Link>
    </main>
  );
}
