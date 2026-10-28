import { useEffect, useRef, type ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { SignInForm } from "@/components/sign-in-form";

export const Route = createFileRoute("/sign-in")({
  component: SignIn,
  head: () => ({ meta: [{ title: "Sign in to inbok" }] }),
});

function SignIn(): ReactElement {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const headingRef = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    headingRef.current?.focus();
  }, []);

  return (
    <main className="mx-auto flex w-full max-w-lg flex-col gap-8 px-5 py-8 wrap-anywhere sm:px-8 sm:py-12">
      <h1 ref={headingRef} tabIndex={-1} className="text-3xl font-bold tracking-tight">
        Sign in to inbok
      </h1>
      <SignInForm
        onDone={() => {
          // A different account must not inherit the previous account's cached data.
          queryClient.clear();
          void navigate({ to: "/", replace: true });
        }}
      />
      <Link className="w-fit py-3 underline underline-offset-4" to="/setup">
        Set up a new installation
      </Link>
    </main>
  );
}
