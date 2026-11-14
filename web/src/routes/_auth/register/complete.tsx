import { useState, type ReactElement } from "react";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { CompleteForm } from "@/features/registration/complete-form";
import { completePath } from "@/features/registration/path";
import type { Ended } from "@/features/registration/visitor-text";
import { useLinkSecret } from "@/hooks/use-link-secret";
import { textLink } from "@/components/link-style";
import { OutcomeLinks, type OutcomeLink } from "@/components/outcome-links";
import { SignedOutPage } from "@/components/signed-out-page";

const newLink: OutcomeLink = { to: "/register", label: "Ask for a new link" };
const signInLink: OutcomeLink = { to: "/sign-in", label: "Sign in" };

export const Route = createFileRoute("/_auth/register/complete")({
  component: CompleteRegistration,
  head: () => ({ meta: [{ title: "Create your account - inbok" }] }),
});

function CompleteRegistration(): ReactElement {
  const navigate = useNavigate();
  const secret = useLinkSecret(completePath);
  // The end of one link: a link pasted into this tab later, which
  // useLinkSecret takes, starts afresh with its own form.
  const [endedLink, setEndedLink] = useState<{ secret: string; end: Ended }>();
  const ended = endedLink?.secret === secret ? endedLink.end : undefined;

  // No session is created: sign-in fills in the email and says why.
  function signIn(email: string): void {
    void navigate({ to: "/sign-in", replace: true, state: { registeredEmail: email } });
  }

  if (!secret)
    return (
      <SignedOutPage
        title="Open the link from your email"
        lead="This page needs the link from your sign-up email."
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
      title="Create your account"
      lead="Enter the address this link was sent to and choose a password."
      step="form"
      after={
        <p className="text-center text-sm text-muted-foreground">
          Link sent to another address?{" "}
          <Link to="/register" activeOptions={{ exact: true }} className={textLink}>
            Ask for a new link
          </Link>
        </p>
      }
    >
      <p className="text-muted-foreground">
        If you didn&apos;t ask to create an account, close this page.
      </p>
      <CompleteForm
        key={secret}
        secret={secret}
        onDone={signIn}
        onEnded={(end) => setEndedLink({ secret, end })}
      />
    </SignedOutPage>
  );
}
