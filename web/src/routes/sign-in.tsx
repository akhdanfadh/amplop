import { useEffect, useRef, useState, type ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, useLocation, useNavigate } from "@tanstack/react-router";
import { useGetRegistration } from "@/api/generated/inbok";
import { SignInForm } from "@/features/auth/sign-in-form";
import { CodeForm } from "@/features/auth/code-form";
import { useSetup } from "@/features/setup/gate";
import { clearAccountCache } from "@/features/auth/session-cache";

const focusClass = "focus-visible:focus-ring";
const noticeId = "sign-in-notice";
const linkClass = `inline-flex min-h-11 w-fit items-center underline underline-offset-4 ${focusClass}`;

export const Route = createFileRoute("/sign-in")({
  component: SignIn,
  head: () => ({ meta: [{ title: "Sign in to inbok" }] }),
});

function SignIn(): ReactElement {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const { completedHere } = useSetup();
  const headingRef = useRef<HTMLHeadingElement>(null);
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
        ? `The account for ${registeredEmail} is created. Sign in with your new password.`
        : resetEmail
          ? `The password for ${resetEmail} is changed. Sign in with your new password.`
          : undefined;
  useEffect(() => {
    headingRef.current?.focus();
  }, [step]);

  function finishSignIn(): void {
    clearAccountCache(queryClient);
    void navigate({ to: "/", replace: true });
  }

  return (
    <main className="mx-auto flex w-full max-w-lg flex-col gap-8 px-5 py-8 wrap-anywhere sm:px-8 sm:py-12">
      {/* Focus lands on the heading, so it reads the notice too. */}
      <h1
        ref={headingRef}
        tabIndex={-1}
        className="text-3xl font-bold tracking-tight"
        aria-describedby={notice ? noticeId : undefined}
      >
        {step === "code" ? "Verify your sign-in" : "Sign in to inbok"}
      </h1>
      <div aria-live="polite" aria-atomic="true" className={completedHere ? undefined : "sr-only"}>
        {completedHere && (
          <section className="flex flex-col gap-3" aria-labelledby="setup-done">
            <h2 id="setup-done" className="text-xl font-semibold">
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
      {notice && <p id={noticeId}>{notice}</p>}
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
      {step === "password" && (
        <div className="flex flex-col">
          <Link to="/forgot-password" className={linkClass}>
            Forgot password?
          </Link>
          {canRegister && (
            <Link to="/register" className={linkClass}>
              Create an account
            </Link>
          )}
        </div>
      )}
    </main>
  );
}
