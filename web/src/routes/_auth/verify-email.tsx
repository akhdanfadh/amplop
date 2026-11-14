import type { ReactElement } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { useLinkSecret } from "@/hooks/use-link-secret";
import { verifyEmailPath } from "@/features/email-verification/path";
import { VerifyEmail } from "@/features/email-verification/verify-email";

export const Route = createFileRoute("/_auth/verify-email")({
  component: VerifyEmailPage,
  head: () => ({ meta: [{ title: "Verify your email address - inbok" }] }),
});

function VerifyEmailPage(): ReactElement {
  const secret = useLinkSecret(verifyEmailPath);

  return <VerifyEmail key={secret} secret={secret} />;
}
