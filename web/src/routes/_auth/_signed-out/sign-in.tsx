import { useState, type ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, useLocation, useNavigate } from "@tanstack/react-router";
import { useGetRegistration } from "@/api/generated/inbok";
import { SignInForm } from "@/features/auth/sign-in-form";
import { CodeForm } from "@/features/auth/code-form";
import { useSetup } from "@/features/setup/gate";
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
  const { completedHere } = useSetup();
  const [step, setStep] = useState<"password" | "code">("password");
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

  function finishSignIn(): void {
    clearAccountCache(queryClient);
    // Back to the page that sent the member here, else the start.
    void navigate({ href: redirect ?? "/", replace: true });
  }

  return (
    <SignedOutPage
      title={step === "code" ? "Verify your sign-in" : "Sign in to inbok"}
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
      <div aria-live="polite" aria-atomic="true" className={completedHere ? undefined : "sr-only"}>
        {completedHere && (
          <section className="flex flex-col gap-2" aria-labelledby="setup-done">
            <h2 id="setup-done" className="text-lg/6 font-semibold">
              Setup complete
            </h2>
            <p>An administrator account has been created.</p>
            <p>
              Remove <code>INBOK_SETUP_TOKEN</code> from the server configuration and restart the
              server.
            </p>
          </section>
        )}
      </div>
      {notice && (
        <p id={noticeId}>
          {/* The address the page before changed, on a line of its own. */}
          <AddressLine>{notice.address}</AddressLine>
          {notice.done} Sign in with your new password.
        </p>
      )}
      {step === "code" ? (
        <>
          <p>Enter the current code from your authenticator app, or an unused backup code.</p>
          <CodeForm onDone={finishSignIn} onRestart={() => setStep("password")} />
        </>
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
