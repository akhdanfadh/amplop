import { useState, type ReactElement } from "react";
import { createFileRoute, Link } from "@tanstack/react-router";
import { RequestForm } from "@/features/password-reset/request-form";
import { requested } from "@/features/password-reset/text";
import { cn } from "@/lib/cn";
import { lineLink } from "@/components/link-style";
import { SignedOutPage } from "@/components/signed-out-page";
import { Button } from "@/components/ui/button";

export const Route = createFileRoute("/_auth/_signed-out/forgot-password")({
  component: ForgotPassword,
  head: () => ({ meta: [{ title: "Reset your password - amplop" }] }),
});

// What the page shows: the form, first or again, or the answer for an
// address.
type Shown =
  | { step: "form"; email: string; shown: "first" | "again" }
  | { step: "sent"; email: string };

function ForgotPassword(): ReactElement {
  const [shown, setShown] = useState<Shown>({ step: "form", email: "", shown: "first" });
  const back = (
    // On a line of its own, so 44 px tall (DESIGN.md "Links").
    <Link to="/sign-in" className={cn(lineLink, "self-center text-sm")}>
      Back to sign in
    </Link>
  );

  if (shown.step === "sent") {
    return (
      <SignedOutPage
        title="Check your email"
        address={shown.email}
        lead={requested.lead}
        step="sent"
        after={back}
      >
        <p>{requested.body}</p>
        <Button
          variant="outline"
          className="whitespace-normal"
          onPress={() => setShown({ step: "form", email: shown.email, shown: "again" })}
        >
          Use a different email address
        </Button>
      </SignedOutPage>
    );
  }

  return (
    <SignedOutPage
      title="Reset your password"
      lead="Enter the email address you sign in with."
      step="form"
      after={back}
    >
      <RequestForm
        email={shown.email}
        shown={shown.shown}
        onSent={(email) => setShown({ step: "sent", email })}
      />
    </SignedOutPage>
  );
}
