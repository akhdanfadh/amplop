import { useState, type ReactElement } from "react";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useGetRegistration } from "@/api/generated/inbok";
import { userMessage } from "@/api/problem";
import { RequestError } from "@/components/request-error";
import { textLink } from "@/components/link-style";
import { SignedOutPage } from "@/components/signed-out-page";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { RequestForm } from "@/features/registration/request-form";
import { requested } from "@/features/registration/visitor-text";

export const Route = createFileRoute("/_auth/_signed-out/register/")({
  component: Register,
  head: () => ({ meta: [{ title: "Create an account - inbok" }] }),
});

// What the page shows once registration is open: the form, first or
// again, or the answer for an address.
type Shown =
  | { step: "form"; email: string; shown: "first" | "again" }
  | { step: "sent"; email: string };

function Register(): ReactElement {
  // Offline still asks, so the page never waits without saying why.
  const availability = useGetRegistration({ query: { networkMode: "always" } });
  const open = availability.data?.data.open;
  const failed = availability.isError && !availability.data;
  const [shown, setShown] = useState<Shown>({ step: "form", email: "", shown: "first" });
  const signIn = (
    <p className="text-center text-sm text-muted-foreground">
      Already have an account?{" "}
      <Link to="/sign-in" className={textLink}>
        Sign in
      </Link>
    </p>
  );

  if (failed)
    return (
      <SignedOutPage
        title="Couldn't check registration"
        lead="inbok couldn't tell whether registration is open."
        step="failed"
        after={signIn}
      >
        {/* The way on, so primary; the error and its reference follow, as
            under a form's button. */}
        <Button onPress={() => void availability.refetch()}>Try again</Button>
        <RequestError
          errors={[{ message: userMessage(availability.error) }]}
          requestId={availability.error.requestId}
        />
      </SignedOutPage>
    );

  // Paused reads as closed: no visitor can register until it resumes.
  if (open === false)
    return (
      <SignedOutPage
        title="Registration is closed"
        lead="This inbok isn't accepting new accounts right now."
        step="closed"
        after={signIn}
      />
    );

  if (open && shown.step === "sent") {
    return (
      <SignedOutPage
        title="Check your email"
        address={shown.email}
        lead={requested.lead}
        step="sent"
        after={signIn}
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
      title="Create an account"
      lead="We'll email you a link to choose your password."
      step={open ? "form" : "checking"}
      after={signIn}
    >
      {open ? (
        <RequestForm
          email={shown.email}
          shown={shown.step === "form" ? shown.shown : "first"}
          onSent={(email) => setShown({ step: "sent", email })}
        />
      ) : (
        // Shown after 300 ms, so a quick check shows nothing (DESIGN.md
        // "States").
        <output className="flex pending-delay items-center gap-2 text-muted-foreground">
          <Spinner />
          Checking whether registration is open...
        </output>
      )}
    </SignedOutPage>
  );
}
