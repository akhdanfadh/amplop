import { useEffect, useRef, type ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Link, createFileRoute, useNavigate } from "@tanstack/react-router";
import { useDeleteCurrentSession, useGetCurrentSession } from "@/api/generated/inbok";
import { uncertainOutcome, userMessage } from "@/api/problem";
import { sessionMessage } from "@/features/auth/session-error";
import { clearAccountCache } from "@/features/auth/session-cache";
import { Button } from "@/components/ui/button";
import { AccountNav } from "@/components/account-nav";

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
    clearAccountCache(queryClient);
    void navigate({ to: "/sign-in", replace: true });
  }

  useEffect(() => {
    if (current.error?.status !== unauthorized || signOut.isPending) return;
    clearAccountCache(queryClient);
    void navigate({ to: "/sign-in", replace: true });
  }, [current.error, signOut.isPending, queryClient, navigate]);

  useEffect(() => {
    if (account?.totpRequired) void navigate({ to: "/security", replace: true });
  }, [account?.totpRequired, navigate]);

  return (
    <>
      <AccountNav account={current.data?.data} />
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
              className="whitespace-normal"
              type="button"
              variant="outline"
              isDisabled={current.isFetching}
              onPress={() => void current.refetch()}
            >
              {current.isFetching ? "Checking your session..." : "Try again"}
            </Button>
          </div>
        )}

        {account && (
          <section className="flex flex-col gap-6" aria-label="Your account">
            {!account.emailVerified && (
              // Password reset emails only a verified address, and its answer
              // cannot say so without telling which accounts exist.
              <p>
                Your email address isn&apos;t verified, so a forgotten password can&apos;t be reset
                by email.{" "}
                <Link
                  to="/security"
                  className="underline underline-offset-4 focus-visible:focus-ring"
                >
                  Verify it in Security
                </Link>
                .
              </p>
            )}
            <Button
              ref={signOutRef}
              className="w-fit whitespace-normal"
              type="button"
              variant="outline"
              isDisabled={signOut.isPending}
              onPress={() => signOut.mutate()}
            >
              {signOut.isPending ? "Signing out..." : "Sign out"}
            </Button>
          </section>
        )}

        {signOut.error && signOut.error.status !== unauthorized && (
          <div className="flex flex-col gap-2" role="alert">
            <p>{sessionMessage(signOut.error, "sign-out")}</p>
            {uncertainOutcome(signOut.error) && (
              <Button
                type="button"
                variant="outline"
                isDisabled={current.isFetching}
                onPress={() => void current.refetch()}
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
    </>
  );
}
