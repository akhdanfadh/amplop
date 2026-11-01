import { useEffect, useRef, useState, type ReactElement } from "react";
import { createFileRoute, useLocation, useNavigate } from "@tanstack/react-router";
import { RedeemForm } from "@/features/email-verification/redeem-form";

export const Route = createFileRoute("/verify-email")({
  component: VerifyEmail,
  head: () => ({ meta: [{ title: "Verify your email address - inbok" }] }),
});

function VerifyEmail(): ReactElement {
  const navigate = useNavigate();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const hash = useLocation({ select: (location) => location.hash });
  // The secret is kept from the first render, before the address bar loses
  // it. Another link pasted into this tab changes only the hash.
  const [secret, setSecret] = useState(hash);
  if (hash && hash !== secret) setSecret(hash);

  useEffect(() => {
    headingRef.current?.focus();
  }, []);
  useEffect(() => {
    // Replacing the entry keeps the secret out of history and copied URLs.
    if (hash) void navigate({ to: "/verify-email", replace: true });
  }, [hash, navigate]);

  return (
    <main className="mx-auto flex w-full max-w-lg flex-col gap-8 px-5 py-8 wrap-anywhere sm:px-8 sm:py-12">
      <h1 ref={headingRef} tabIndex={-1} className="text-3xl font-bold tracking-tight">
        Verify your email address
      </h1>
      <RedeemForm key={secret} secret={secret} />
    </main>
  );
}
