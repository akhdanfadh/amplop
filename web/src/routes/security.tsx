import { useEffect, useRef, useState } from "react";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useGetCurrentSession, useDeleteCurrentSession } from "@/api/generated/inbok";
import { userMessage } from "@/api/problem";
import { clearAccountCache } from "@/features/auth/session-cache";
import { sessionMessage } from "@/features/auth/session-error";
import { Enrollment } from "@/features/totp/enrollment";
import { AccountNav } from "@/components/account-nav";
import { Button } from "@/components/ui/button";

const unauthorized = 401;

export const Route = createFileRoute("/security")({
  component: Security,
  head: () => ({ meta: [{ title: "Account security - inbok" }] }),
});

function Security() {
  const client = useQueryClient();
  const navigate = useNavigate();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const [writing, setWriting] = useState(false);
  const [hasCodes, setHasCodes] = useState(false);
  const signOut = useDeleteCurrentSession({
    mutation: {
      onMutate: () => client.cancelQueries(),
      onSuccess: finishSignOut,
      onError: (error) => {
        if (error.status === unauthorized) finishSignOut();
      },
    },
  });
  const current = useGetCurrentSession({
    query: { enabled: !writing && !signOut.isPending, networkMode: "always" },
  });
  const ready = current.isSuccess && !current.isFetching && !current.isPaused;

  function finishSignOut(): void {
    clearAccountCache(client);
    void navigate({ to: "/sign-in", replace: true, ignoreBlocker: true });
  }

  useEffect(() => {
    headingRef.current?.focus();
  }, []);
  useEffect(() => {
    if (current.error?.status !== unauthorized) return;
    clearAccountCache(client);
    void navigate({ to: "/sign-in", replace: true, ignoreBlocker: true });
  }, [current.error, client, navigate]);

  return (
    <>
      <AccountNav />
      <main className="mx-auto flex w-full max-w-lg flex-col gap-8 px-5 py-8 wrap-anywhere sm:px-8 sm:py-12">
        <h1 ref={headingRef} tabIndex={-1} className="text-3xl font-bold tracking-tight">
          Account security
        </h1>
        {current.isFetching && <output>Checking your session...</output>}
        {current.error && current.error.status !== unauthorized && (
          <div className="flex flex-col gap-3" role="alert">
            <p>Could not check your session. {userMessage(current.error)}</p>
            <Button
              type="button"
              variant="outline"
              className="min-h-11 border-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
              disabled={current.isFetching}
              onClick={() => void current.refetch()}
            >
              Try again
            </Button>
          </div>
        )}
        {current.data && current.error?.status !== unauthorized && (
          // Preserve one-time codes while the renewed session is being checked.
          <div hidden={!ready}>
            {current.data.data.totpRequired && (
              <p className="mb-6">
                Set up two-factor authentication to unlock administrator access.
              </p>
            )}
            <Enrollment
              key={current.data.data.email}
              locked={!ready}
              onWritingChange={setWriting}
              onCodesChange={setHasCodes}
            />
          </div>
        )}
        {current.data && (
          <Button
            type="button"
            variant="outline"
            className="min-h-11 w-fit border-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
            disabled={!ready || writing || hasCodes || signOut.isPending}
            onClick={() => signOut.mutate()}
          >
            {signOut.isPending ? "Signing out..." : "Sign out"}
          </Button>
        )}
        {signOut.error && <p role="alert">{sessionMessage(signOut.error, "sign-out")}</p>}
      </main>
    </>
  );
}
