import { useEffect, useRef, type ReactElement } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { useSetup } from "@/components/setup-gate";
import { userMessage } from "@/api/problem";
import { SetupForm } from "@/components/setup-form";
import { Button } from "@/components/ui/button";

export const Route = createFileRoute("/setup")({
  component: Setup,
  head: () => ({ meta: [{ title: "Set up inbok" }] }),
});

function Setup(): ReactElement {
  const { status, attempt, complete } = useSetup();
  const current = status.data?.data.status;
  const headingRef = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    headingRef.current?.focus();
  }, []);

  return (
    <main className="mx-auto flex w-full max-w-lg flex-col gap-8 px-5 py-8 wrap-anywhere sm:px-8 sm:py-12">
      <h1 ref={headingRef} tabIndex={-1} className="text-3xl font-bold tracking-tight">
        Set up inbok
      </h1>

      {current === "unconfigured" && (
        <section className="space-y-4" aria-labelledby="setup-unconfigured">
          <h2 id="setup-unconfigured" className="text-xl font-semibold">
            Configure a setup token
          </h2>
          <p className="leading-relaxed">
            Set <code className="break-all">INBOK_SETUP_TOKEN</code> on the server to a random token
            of at least 32 characters, then restart the server.
          </p>
          <p className="leading-relaxed">
            Generate a token with <code>openssl rand -base64 32</code>. Keep it for the next step.
          </p>
        </section>
      )}

      {current === "open" && (
        <section className="space-y-6" aria-labelledby="setup-open">
          <div className="space-y-2">
            <h2 id="setup-open" className="text-xl font-semibold">
              Create the first administrator
            </h2>
            <p className="leading-relaxed text-muted-foreground">
              Use the setup token from the server configuration to create the account.
            </p>
          </div>
          <SetupForm onAttempt={attempt} onDone={complete} />
        </section>
      )}

      {status.isError && (
        <div className="space-y-2" role="alert">
          <p>Could not check setup status. {userMessage(status.error)}</p>
          {status.error.requestId && (
            <p className="text-sm text-muted-foreground">
              Request reference: <code className="break-all">{status.error.requestId}</code>
            </p>
          )}
        </div>
      )}

      {current !== "done" && !status.isPending && (
        <Button
          className="h-auto min-h-11 border-muted-foreground whitespace-normal focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid dark:border-muted-foreground"
          type="button"
          variant="outline"
          disabled={status.isFetching}
          onClick={() => void status.refetch()}
        >
          {status.isFetching
            ? "Checking setup status..."
            : status.isError
              ? "Try again"
              : "Check setup status"}
        </Button>
      )}
    </main>
  );
}
