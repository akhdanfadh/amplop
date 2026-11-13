import { useEffect, useRef, useState } from "react";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useGetCurrentSession, useDeleteCurrentSession } from "@/api/generated/inbok";
import { userMessage } from "@/api/problem";
import { clearAccountCache } from "@/features/auth/session-cache";
import { sessionMessage } from "@/features/auth/session-error";
import { EmailVerificationSection } from "@/features/email-verification/section";
import { SessionList } from "@/features/sessions/list";
import { Enrollment } from "@/features/totp/enrollment";
import { Button } from "@/components/ui/button";

const unauthorized = 401;

export const Route = createFileRoute("/_app/settings/security")({
  component: Security,
  head: () => ({ meta: [{ title: "Account security - inbok" }] }),
});

function Security() {
  const client = useQueryClient();
  const navigate = useNavigate();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const [writing, setWriting] = useState(false);
  const [ending, setEnding] = useState(false);
  const [sending, setSending] = useState(false);
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
    <main className="mx-auto flex w-full max-w-lg flex-col gap-8 px-5 py-8 wrap-anywhere sm:px-8 sm:py-12">
      <h1 ref={headingRef} tabIndex={-1} className="text-3xl font-bold tracking-tight">
        Account security
      </h1>
      {/* Only a first check says so: DESIGN.md has background re-reads
          show nothing, and the text would push the page down and back. */}
      {current.isFetching && !current.data && <output>Checking your session...</output>}
      {current.error && current.error.status !== unauthorized && (
        <div className="flex flex-col gap-3" role="alert">
          <p>Could not check your session. {userMessage(current.error)}</p>
          <Button
            type="button"
            variant="outline"
            isDisabled={current.isFetching}
            onPress={() => void current.refetch()}
          >
            Try again
          </Button>
        </div>
      )}
      {current.data && current.error?.status !== unauthorized && (
        // Stay mounted to keep one-time codes; stay shown during background
        // checks so a code field keeps focus. Enrollment and SessionList lock
        // each other: confirming TOTP replaces this session's cookie. So do
        // Enrollment and EmailVerificationSection.
        <div className="flex flex-col gap-8" hidden={!current.isSuccess}>
          <EmailVerificationSection
            key={current.data.data.email}
            email={current.data.data.email}
            verified={current.data.data.emailVerified}
            locked={!ready || writing || signOut.isPending}
            onWritingChange={setSending}
          />
          <div>
            {current.data.data.totpRequired && (
              <p className="mb-6">
                Set up two-factor authentication to unlock administrator access.
              </p>
            )}
            <Enrollment
              key={current.data.data.email}
              role={current.data.data.role}
              locked={!ready || ending || sending}
              onWritingChange={setWriting}
              onCodesChange={setHasCodes}
            />
          </div>
          <SessionList
            locked={!ready || writing || signOut.isPending}
            paused={writing || signOut.isPending}
            onWritingChange={setEnding}
          />
        </div>
      )}
      {current.data && (
        <Button
          type="button"
          variant="outline"
          className="w-fit"
          isDisabled={!ready || writing || ending || sending || hasCodes || signOut.isPending}
          onPress={() => signOut.mutate()}
        >
          {signOut.isPending ? "Signing out..." : "Sign out of this browser"}
        </Button>
      )}
      {signOut.error && <p role="alert">{sessionMessage(signOut.error, "sign-out")}</p>}
    </main>
  );
}
