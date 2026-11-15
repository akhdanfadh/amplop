import type { ReactElement } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { userMessage } from "@/api/problem";
import { RequestError } from "@/components/request-error";
import { SignedOutPage } from "@/components/signed-out-page";
import { Button } from "@/components/ui/button";
import { SetupForm } from "@/features/setup/form";
import { useSetup } from "@/features/setup/gate";

export const Route = createFileRoute("/_auth/setup")({
  component: Setup,
  head: () => ({ meta: [{ title: "Set up inbok" }] }),
});

// Setup shows how to give the server a setup token, then the form that
// creates the first administrator with it. SetupGate shows this page only
// while setup is not done, and handles a first status read that fails.
function Setup(): ReactElement {
  const { status, attempt, complete } = useSetup();
  const current = status.data?.data.status;
  // A later recheck that fails keeps the page and says so.
  const recheckFailed = status.isError && (
    <RequestError
      errors={[{ message: `Could not check setup status. ${userMessage(status.error)}` }]}
      requestId={status.error.requestId}
    />
  );

  if (current === "unconfigured")
    return (
      <SignedOutPage
        title="Configure a setup token"
        lead="The server needs one before anyone can set up inbok."
        step="unconfigured"
      >
        <p>
          Set <code className="break-all">INBOK_SETUP_TOKEN</code> on the server to a random token
          of at least 32 characters, then restart the server.
        </p>
        <p>
          Generate a token with <code className="whitespace-nowrap">openssl rand -base64 32</code>.
          Keep it for the next step.
        </p>
        <Button
          className="whitespace-normal"
          isPending={status.isFetching}
          onPress={() => void status.refetch()}
        >
          {status.isFetching ? "Checking setup status..." : "Check setup status"}
        </Button>
        {recheckFailed}
      </SignedOutPage>
    );

  return (
    <SignedOutPage
      title="Set up inbok"
      lead="Create the first administrator with the server's setup token."
      step="open"
    >
      {recheckFailed}
      <SetupForm onAttempt={attempt} onDone={complete} />
    </SignedOutPage>
  );
}
