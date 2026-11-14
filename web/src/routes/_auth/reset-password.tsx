import { useState, type ReactElement } from "react";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { resetPath } from "@/features/password-reset/path";
import { ResetForm } from "@/features/password-reset/reset-form";
import type { Ended } from "@/features/password-reset/text";
import { useLinkSecret } from "@/hooks/use-link-secret";
import { textLink } from "@/components/link-style";
import { OutcomeLinks, type OutcomeLink } from "@/components/outcome-links";
import { SignedOutPage } from "@/components/signed-out-page";

const newLink: OutcomeLink = { to: "/forgot-password", label: "Ask for a new link" };
const signInLink: OutcomeLink = { to: "/sign-in", label: "Sign in" };

export const Route = createFileRoute("/_auth/reset-password")({
  component: ResetPassword,
  head: () => ({ meta: [{ title: "Choose a new password - inbok" }] }),
});

function ResetPassword(): ReactElement {
  const navigate = useNavigate();
  const secret = useLinkSecret(resetPath);
  // The end of one link: a link pasted into this tab later, which
  // useLinkSecret takes, starts afresh with its own form.
  const [endedLink, setEndedLink] = useState<{ secret: string; end: Ended }>();
  const ended = endedLink?.secret === secret ? endedLink.end : undefined;

  // signIn opens sign-in for email, the account just reset, in place of this
  // page.
  function signIn(email: string): void {
    // No session is created, so sign-in fills in the email and says why.
    // Replacing the entry keeps Back from returning to a spent link.
    void navigate({ to: "/sign-in", replace: true, state: { passwordResetEmail: email } });
  }

  if (!secret)
    return (
      <SignedOutPage
        title="Open the link from your email"
        lead="This page needs the link from your password reset email."
        step="no-link"
      >
        <OutcomeLinks links={[newLink]} />
      </SignedOutPage>
    );

  // No retry with this link can work, so the form goes.
  if (ended)
    return (
      <SignedOutPage title={ended.title} step="ended">
        <p>{ended.message}</p>
        <OutcomeLinks
          links={ended.links.map((link) => (link === "new-link" ? newLink : signInLink))}
        />
      </SignedOutPage>
    );

  return (
    <SignedOutPage
      title="Choose a new password"
      lead="Changing it signs your account out on every browser."
      step="form"
      after={
        <p className="text-center text-sm text-muted-foreground">
          Link sent to another address?{" "}
          <Link to="/forgot-password" className={textLink}>
            Ask for a new link
          </Link>
        </p>
      }
    >
      <p className="text-muted-foreground">
        If you sign in with an authenticator app, you&apos;ll still need its code.
      </p>
      <ResetForm
        key={secret}
        secret={secret}
        onDone={signIn}
        onEnded={(end) => setEndedLink({ secret, end })}
      />
    </SignedOutPage>
  );
}
