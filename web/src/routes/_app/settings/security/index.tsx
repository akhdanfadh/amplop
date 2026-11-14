import { useEffect, useState, type ReactElement } from "react";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useDeleteCurrentSession, useGetCurrentSession } from "@/api/generated/inbok";
import { userMessage } from "@/api/problem";
import { ReadError } from "@/components/read-error";
import { Button } from "@/components/ui/button";
import { clearAccountCache } from "@/features/auth/session-cache";
import { sessionMessage } from "@/features/auth/session-error";
import { EmailVerificationSection } from "@/features/email-verification/section";
import { SessionList } from "@/features/sessions/list";
import { SettingsPage } from "@/features/settings/page";
import { pageName, securityLead } from "@/features/settings/text";
import { TwoFactorSection } from "@/features/totp/security-section";
import { adminReason } from "@/features/totp/text";

const unauthorized = 401;

export const Route = createFileRoute("/_app/settings/security/")({
  component: Security,
  head: () => ({ meta: [{ title: "Security - inbok" }] }),
});

// Security is the account's security settings: the email address and its
// verification, two-factor authentication, which opens its own page, and
// the signed-in browsers.
function Security(): ReactElement {
  const client = useQueryClient();
  const navigate = useNavigate();
  const [ending, setEnding] = useState(false);
  const [sending, setSending] = useState(false);
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
    query: { enabled: !signOut.isPending, networkMode: "always" },
  });
  const ready = current.isSuccess && !current.isFetching && !current.isPaused;

  function finishSignOut(): void {
    clearAccountCache(client);
    void navigate({ to: "/sign-in", replace: true, ignoreBlocker: true });
  }

  useEffect(() => {
    if (current.error?.status !== unauthorized) return;
    clearAccountCache(client);
    void navigate({ to: "/sign-in", replace: true, ignoreBlocker: true });
  }, [current.error, client, navigate]);

  return (
    <SettingsPage title={pageName.security} lead={securityLead}>
      {/* Only a first check says so: DESIGN.md has background re-reads
          show nothing, and the text would push the page down and back. */}
      {current.isFetching && !current.data && (
        <output className="block py-6">Checking your session...</output>
      )}
      {/* A failed refresh keeps the sections, whose writes wait for a
          successful read, and says why (DESIGN.md "States"). */}
      {current.error && current.error.status !== unauthorized && (
        <div className="py-6">
          <ReadError
            message={`Could not check your session. ${userMessage(current.error)}`}
            error={current.error}
            retrying={current.isFetching}
            onRetry={() => void current.refetch()}
          />
        </div>
      )}
      {current.data && (
        <>
          <EmailVerificationSection
            key={current.data.data.email}
            email={current.data.data.email}
            verified={current.data.data.emailVerified}
            locked={!ready || signOut.isPending}
            onWritingChange={setSending}
          />
          <TwoFactorSection>
            {current.data.data.totpRequired && (
              <p className="text-sm text-muted-foreground">{adminReason}</p>
            )}
          </TwoFactorSection>
          <SessionList
            locked={!ready || signOut.isPending}
            paused={signOut.isPending}
            onWritingChange={setEnding}
          >
            <Button
              type="button"
              variant="outline"
              className="whitespace-normal"
              isDisabled={!ready || ending || sending}
              isPending={signOut.isPending}
              onPress={() => signOut.mutate()}
            >
              {signOut.isPending ? "Signing out..." : "Sign out of this browser"}
            </Button>
            {signOut.error && <p role="alert">{sessionMessage(signOut.error, "sign-out")}</p>}
          </SessionList>
        </>
      )}
    </SettingsPage>
  );
}
