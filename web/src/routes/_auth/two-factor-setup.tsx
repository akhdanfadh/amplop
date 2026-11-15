import { useCallback, useRef, useState, type ReactElement } from "react";
import { createFileRoute, redirect, useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useDeleteCurrentSession } from "@/api/generated/inbok";
import type { ApiError } from "@/api/transport";
import { cn } from "@/lib/cn";
import { smallTarget } from "@/components/link-style";
import { SignedOutPage } from "@/components/signed-out-page";
import { Button } from "@/components/ui/button";
import { readSession } from "@/features/auth/guard";
import { clearAccountCache } from "@/features/auth/session-cache";
import { sessionMessage } from "@/features/auth/session-error";
import { SetupDoneNotice } from "@/features/setup/done-notice";
import { useSetupFlow } from "@/features/totp/setup";
import { adminSetupLead, cardSignOut, changeNotice, setUpName } from "@/features/totp/text";

const unauthorized = 401;

// An administrator without two-factor authentication may only set it up:
// the server refuses everything else, so the app sends them here, a
// signed-out style card with nothing else to reach. Without a session the
// page is sign-in's; with two-factor on, the app's.
export const Route = createFileRoute("/_auth/two-factor-setup")({
  beforeLoad: async ({ context }) => {
    const session = await readSession(context.queryClient);
    if (!session) throw redirect({ to: "/sign-in", replace: true });
    if (!session.totpRequired) throw redirect({ to: "/", replace: true });
    return { session };
  },
  component: TwoFactorSetup,
  head: () => ({ meta: [{ title: `${setUpName} - inbok` }] }),
});

// TwoFactorSetup is the setup's steps in a card, with a way to sign out
// instead. Finishing opens Security, which says two-factor is on.
function TwoFactorSetup(): ReactElement {
  const { session } = Route.useRouteContext();
  const client = useQueryClient();
  const navigate = useNavigate();
  const [canceled, setCanceled] = useState(false);
  // Where the card is going: nowhere yet, signing out, or gone. Sign out,
  // once pressed, wins over a finish seen meanwhile, and the card leaves
  // once, so a later answer cannot send it elsewhere.
  const exit = useRef<"staying" | "signing-out" | "left">("staying");

  const toSignIn = useCallback(() => {
    if (exit.current === "left") return;
    exit.current = "left";
    clearAccountCache(client);
    void navigate({ to: "/sign-in", replace: true, ignoreBlocker: true });
  }, [client, navigate]);

  const toSecurity = useCallback(() => {
    if (exit.current !== "staying") return;
    exit.current = "left";
    void navigate({
      to: "/settings/security",
      state: { twoFactorChange: "on" },
      replace: true,
      ignoreBlocker: true,
    });
  }, [navigate]);

  // A 401 from any request here: the session ended, e.g. its 15 minutes
  // ran out or another browser signed it out.
  const onError = useCallback(
    (err: ApiError) => {
      if (err.status === unauthorized) toSignIn();
    },
    [toSignIn],
  );
  // The callbacks sit on the hook, not on mutate(), so they run even if
  // the card has gone when the answer comes.
  const signOut = useDeleteCurrentSession({
    mutation: {
      onMutate: () => {
        exit.current = "signing-out";
        return client.cancelQueries();
      },
      onSuccess: toSignIn,
      onError: (error) => {
        if (error.status === unauthorized) toSignIn();
        else if (exit.current === "signing-out") exit.current = "staying";
      },
    },
  });

  const { stage, content } = useSetupFlow({
    email: session.email,
    onError,
    onFinished: toSecurity,
    onCanceled: () => setCanceled(true),
  });

  return (
    <SignedOutPage
      // A non-breaking hyphen keeps "two-factor" whole where the title
      // wraps in the narrow card.
      title={setUpName.replace("-", "\u2011")}
      // The account signed in, which Sign out under the card ends.
      address={session.email}
      lead={adminSetupLead}
      step={stage}
      after={
        <div className="flex flex-col items-center gap-2 text-center text-sm text-muted-foreground">
          <p>
            {cardSignOut.question}{" "}
            <Button
              type="button"
              variant="link"
              className={cn(smallTarget, "p-0 font-normal")}
              // Pending, not disabled: it keeps focus, so a failed sign-out
              // can be tried again with Enter.
              isPending={signOut.isPending}
              onPress={() => signOut.mutate()}
            >
              {signOut.isPending ? cardSignOut.working : cardSignOut.label}
            </Button>
          </p>
          {signOut.error && signOut.error.status !== unauthorized && (
            <p role="alert">{sessionMessage(signOut.error, "sign-out")}</p>
          )}
        </div>
      }
    >
      <SetupDoneNotice />
      {canceled && stage === "password" && <output>{changeNotice.canceled}</output>}
      {content}
    </SignedOutPage>
  );
}
