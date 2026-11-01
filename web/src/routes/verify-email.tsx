import { useEffect, useRef, useState, type ReactElement } from "react";
import { createFileRoute, useLocation, useNavigate } from "@tanstack/react-router";
import { forgetLinkSecret, linkSecret } from "@/features/email-verification/link-secret";
import { RedeemForm } from "@/features/email-verification/redeem-form";

export const Route = createFileRoute("/verify-email")({
  component: VerifyEmail,
  head: () => ({ meta: [{ title: "Verify your email address - inbok" }] }),
});

function VerifyEmail(): ReactElement {
  const navigate = useNavigate();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const hash = useLocation({ select: (location) => location.hash });
  // getRouter took the opening link's secret out of the address at start; a
  // link pasted into this tab later changes only the hash, which wins.
  const [secret, setSecret] = useState(() => hash || linkSecret());
  if (hash && hash !== secret) setSecret(hash);

  useEffect(() => {
    // A later visit without a link must not reuse this one.
    forgetLinkSecret();
    headingRef.current?.focus();
  }, []);
  useEffect(() => {
    // Replacing the entry keeps a pasted link's secret out of the address
    // bar, Back, and copied URLs.
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
