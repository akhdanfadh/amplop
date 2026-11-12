import { useEffect, useRef, type ReactElement } from "react";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useGetRegistration } from "@/api/generated/inbok";
import { userMessage } from "@/api/problem";
import { Button } from "@/components/ui/button";
import { RequestForm } from "@/features/registration/request-form";

const focusClass = "focus-visible:focus-ring";
const linkClass = `inline-flex min-h-11 w-fit items-center underline underline-offset-4 ${focusClass}`;

// Shown is the last answer the page showed about registration, "none"
// before the first.
type Shown = "none" | "error" | "open" | "closed";

export const Route = createFileRoute("/register/")({
  component: Register,
  head: () => ({ meta: [{ title: "Create an account - inbok" }] }),
});

function Register(): ReactElement {
  const headingRef = useRef<HTMLHeadingElement>(null);
  const closedRef = useRef<HTMLParagraphElement>(null);
  const retryRef = useRef<HTMLButtonElement>(null);
  // Offline still asks, so the page never waits without saying why.
  const availability = useGetRegistration({ query: { networkMode: "always" } });
  const open = availability.data?.data.open;
  const failed = availability.isError && !availability.data;
  const shown = useRef<Shown>("none");

  useEffect(() => {
    headingRef.current?.focus();
  }, []);

  // The form or the error block can unmount while focused: a refused
  // request finds registration closed, or a retry, which shows "Checking"
  // first because no status was read yet, ends. Focus would fall to the
  // page, so the new content takes it. The first check keeps the heading's.
  useEffect(() => {
    if (!failed && open === undefined) return;
    const now: Shown = failed ? "error" : open ? "open" : "closed";
    const first = shown.current === "none";
    shown.current = now;
    if (first) return;

    if (now === "error") retryRef.current?.focus();
    if (now === "closed") closedRef.current?.focus();
    if (now === "open") headingRef.current?.focus();
  }, [failed, open]);

  return (
    <main className="mx-auto flex w-full max-w-lg flex-col gap-8 px-5 py-8 wrap-anywhere sm:px-8 sm:py-12">
      <h1 ref={headingRef} tabIndex={-1} className="text-3xl font-bold tracking-tight">
        Create an account
      </h1>

      {availability.isPending && <output>Checking whether registration is open...</output>}

      {failed && (
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-2" role="alert">
            <p>Could not check whether registration is open. {userMessage(availability.error)}</p>
            {availability.error.requestId && (
              <p className="text-sm text-muted-foreground">
                Request reference: <code className="break-all">{availability.error.requestId}</code>
              </p>
            )}
          </div>
          <Button
            ref={retryRef}
            type="button"
            variant="outline"
            className="w-fit"
            onPress={() => void availability.refetch()}
          >
            Try again
          </Button>
        </div>
      )}

      {/* Paused reads as closed: no visitor can register until it resumes. */}
      {open === false && (
        <p ref={closedRef} tabIndex={-1}>
          This inbok isn&apos;t accepting new accounts right now.
        </p>
      )}

      {open && <RequestForm />}

      <Link to="/sign-in" className={linkClass}>
        Sign in to an existing account
      </Link>
    </main>
  );
}
