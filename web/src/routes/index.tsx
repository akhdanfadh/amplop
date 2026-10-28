import { useEffect, useRef, type ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useDeleteCurrentSession, useGetCurrentSession } from "@/api/generated/inbok";
import { userMessage } from "@/api/problem";
import { sessionMessage, uncertainSession } from "@/api/session-error";
import { Button } from "@/components/ui/button";

const unauthorized = 401;

export const Route = createFileRoute("/")({ component: Home });

function Home(): ReactElement {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const signOutRef = useRef<HTMLButtonElement>(null);
  const retryRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    headingRef.current?.focus();
  }, []);
  const signOut = useDeleteCurrentSession({
    mutation: {
      onSuccess: finishSignOut,
      onError: (err) => {
        // An absent or expired session is already signed out; Go clears its cookie.
        if (err.status === unauthorized) finishSignOut();
      },
    },
  });
  const current = useGetCurrentSession({ query: { enabled: !signOut.isPending } });
  // Cached identity may belong to a cookie another tab has replaced.
  const account =
    current.isError || current.isFetching || current.isPaused ? undefined : current.data?.data;

  useEffect(() => {
    if (signOut.error && !signOut.isPending && !current.isFetching)
      (signOutRef.current ?? retryRef.current)?.focus();
  }, [signOut.error, signOut.isPending, current.error, current.isFetching]);

  function finishSignOut(): void {
    queryClient.clear();
    void navigate({ to: "/sign-in", replace: true });
  }

  useEffect(() => {
    if (current.error?.status !== unauthorized || signOut.isPending) return;
    queryClient.clear();
    void navigate({ to: "/sign-in", replace: true });
  }, [current.error, signOut.isPending, queryClient, navigate]);

  return (
    <main className="mx-auto flex w-full max-w-lg flex-col gap-8 px-5 py-8 wrap-anywhere sm:px-8 sm:py-12">
      <h1 ref={headingRef} tabIndex={-1} className="text-3xl font-bold tracking-tight">
        inbok
      </h1>

      <output aria-atomic="true">
        {current.isPending && <span>Checking your session...</span>}
        {current.isPaused && <span>Waiting for a connection to check your session...</span>}
        {current.isFetching && !current.isPending && <span>Checking your session... </span>}
        {current.error?.status === unauthorized && <span>Opening sign-in...</span>}
        {account && (
          <span>
            Signed in as <span className="font-medium">{account.email}</span>
          </span>
        )}
      </output>

      {current.error && current.error.status !== unauthorized && (
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-2" role="alert">
            <p>Could not check your session. {userMessage(current.error)}</p>
            {current.error.requestId && (
              <p className="text-sm text-muted-foreground">
                Request reference: <code className="break-all">{current.error.requestId}</code>
              </p>
            )}
          </div>
          <Button
            ref={retryRef}
            className="h-auto min-h-11 border-muted-foreground whitespace-normal focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
            type="button"
            variant="outline"
            disabled={current.isFetching}
            onClick={() => void current.refetch()}
          >
            {current.isFetching ? "Checking your session..." : "Try again"}
          </Button>
        </div>
      )}

      {account && (
        <section className="flex flex-col gap-6" aria-label="Your account">
          {account.totpRequired && (
            <p className="leading-relaxed">
              Two-factor authentication enrollment comes next. Your account has member access until
              it is set up.
            </p>
          )}
          <Button
            ref={signOutRef}
            className="h-auto min-h-11 w-fit whitespace-normal focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
            type="button"
            variant="outline"
            disabled={signOut.isPending}
            onClick={() => signOut.mutate()}
          >
            {signOut.isPending ? "Signing out..." : "Sign out"}
          </Button>
        </section>
      )}

      {signOut.error && signOut.error.status !== unauthorized && (
        <div className="flex flex-col gap-2" role="alert">
          <p>{sessionMessage(signOut.error, "sign-out")}</p>
          {uncertainSession(signOut.error) && (
            <Button
              type="button"
              variant="outline"
              disabled={current.isFetching}
              className="min-h-11 border-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
              onClick={() => void current.refetch()}
            >
              {current.isFetching ? "Checking your session..." : "Check session"}
            </Button>
          )}
          {signOut.error.requestId && (
            <p className="text-sm text-muted-foreground">
              Request reference: <code className="break-all">{signOut.error.requestId}</code>
            </p>
          )}
        </div>
      )}
    </main>
  );
}
