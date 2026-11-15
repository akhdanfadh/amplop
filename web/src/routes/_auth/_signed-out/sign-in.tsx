import { useState, type ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, useLocation, useNavigate } from "@tanstack/react-router";
import { useGetRegistration } from "@/api/generated/inbok";
import { SignInForm } from "@/features/auth/sign-in-form";
import { userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import { Button } from "@/components/ui/button";
import { CodeForm } from "@/features/auth/code-form";
import { SetupDoneNotice } from "@/features/setup/done-notice";
import { signInSearch } from "@/features/auth/search";
import { clearAccountCache } from "@/features/auth/session-cache";
import { textLink } from "@/components/link-style";
import { AddressLine, SignedOutPage } from "@/components/signed-out-page";

const noticeId = "sign-in-notice";

export const Route = createFileRoute("/_auth/_signed-out/sign-in")({
  validateSearch: signInSearch,
  component: SignIn,
  head: () => ({ meta: [{ title: "Sign in to inbok" }] }),
});

function SignIn(): ReactElement {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const { redirect } = Route.useSearch();
  const [step, setStep] = useState<"password" | "code" | "ended">("password");
  // Why the code step ended the attempt, for its end screen.
  const [ended, setEnded] = useState<ApiError>();
  // Set by /register/complete and /reset-password, which create no session.
  const registeredEmail = useLocation({ select: (location) => location.state.registeredEmail });
  const resetEmail = useLocation({ select: (location) => location.state.passwordResetEmail });
  // A failed read hides the link: sign-in matters more than registering.
  const registration = useGetRegistration();
  const canRegister = step === "password" && registration.data?.data.open;
  // Naming the email confirms which account the page before changed.
  const notice =
    step !== "password"
      ? undefined
      : registeredEmail
        ? { address: registeredEmail, done: "Your account is created." }
        : resetEmail
          ? { address: resetEmail, done: "Your password is changed." }
          : undefined;

  function restart(): void {
    setEnded(undefined);
    setStep("password");
  }

  function finishSignIn(): void {
    clearAccountCache(queryClient);
    // Back to the page that sent the member here, else the start.
    void navigate({ href: redirect ?? "/", replace: true });
  }

  // The attempt ended (expired, used, or the account deactivated): only
  // starting again can continue (DESIGN.md "Outcome screens").
  if (step === "ended" && ended)
    return (
      <SignedOutPage title="This sign-in has ended" step="ended">
        <p>{userMessage(ended)}</p>
        <Button className="whitespace-normal" onPress={restart}>
          Start sign-in again
        </Button>
      </SignedOutPage>
    );

  return (
    <SignedOutPage
      title={step === "code" ? "Verify your sign-in" : "Sign in to inbok"}
      // The code step's instruction is its field's description: the field
      // takes focus there (DESIGN.md "Focus").
      lead={step === "code" ? undefined : "Use your email address and password."}
      step={step}
      // The title takes focus, so it reads the notice too.
      describedBy={notice ? noticeId : undefined}
      after={
        canRegister && (
          <p className="text-center text-sm text-muted-foreground">
            No account yet?{" "}
            <Link to="/register" className={textLink}>
              Create an account
            </Link>
          </p>
        )
      }
    >
      <SetupDoneNotice />
      {notice && (
        <p id={noticeId}>
          {/* The address the page before changed, on a line of its own. */}
          <AddressLine>{notice.address}</AddressLine>
          {notice.done} Sign in with your new password.
        </p>
      )}
      {step === "code" ? (
        <CodeForm
          onDone={finishSignIn}
          onEnded={(err) => {
            setEnded(err);
            setStep("ended");
          }}
          onRestart={restart}
        />
      ) : (
        <SignInForm
          email={registeredEmail ?? resetEmail}
          onDone={finishSignIn}
          onCodeRequired={() => setStep("code")}
        />
      )}
    </SignedOutPage>
  );
}
