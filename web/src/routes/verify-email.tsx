import { useEffect, useRef, type ReactElement } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { useLinkSecret } from "@/hooks/use-link-secret";
import { verifyEmailPath } from "@/features/email-verification/path";
import { RedeemForm } from "@/features/email-verification/redeem-form";

export const Route = createFileRoute("/verify-email")({
  component: VerifyEmail,
  head: () => ({ meta: [{ title: "Verify your email address - inbok" }] }),
});

function VerifyEmail(): ReactElement {
  const headingRef = useRef<HTMLHeadingElement>(null);
  const secret = useLinkSecret(verifyEmailPath);

  useEffect(() => {
    headingRef.current?.focus();
  }, []);

  return (
    <main className="mx-auto flex w-full max-w-lg flex-col gap-8 px-5 py-8 wrap-anywhere sm:px-8 sm:py-12">
      <h1 ref={headingRef} tabIndex={-1} className="text-3xl font-bold tracking-tight">
        Verify your email address
      </h1>
      <RedeemForm key={secret} secret={secret} />
    </main>
  );
}
