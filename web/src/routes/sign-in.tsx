import { useEffect, useRef, useState, type ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useGetRegistration } from "@/api/generated/inbok";
import { SignInForm } from "@/features/auth/sign-in-form";
import { CodeForm } from "@/features/auth/code-form";
import { useSetup } from "@/features/setup/gate";
import { clearAccountCache } from "@/features/auth/session-cache";

const focusClass =
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid";
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
  // A failed read hides the link: sign-in matters more than registering.
  const registration = useGetRegistration();
  const canRegister = step === "password" && registration.data?.data.open;
  useEffect(() => {
    headingRef.current?.focus();
  }, [step]);

  function finishSignIn(): void {
    clearAccountCache(queryClient);
    void navigate({ to: "/", replace: true });
  }

  return (
    <main className="mx-auto flex w-full max-w-lg flex-col gap-8 px-5 py-8 wrap-anywhere sm:px-8 sm:py-12">
      <h1 ref={headingRef} tabIndex={-1} className="text-3xl font-bold tracking-tight">
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
      {step === "code" ? (
        <>
          <p>Enter the current code from your authenticator app, or an unused backup code.</p>
          <CodeForm onDone={finishSignIn} onRestart={() => setStep("password")} />
        </>
      ) : (
        <SignInForm onDone={finishSignIn} onCodeRequired={() => setStep("code")} />
      )}
      {canRegister && (
        <Link to="/register" className={linkClass}>
          Create an account
        </Link>
      )}
    </main>
  );
}
