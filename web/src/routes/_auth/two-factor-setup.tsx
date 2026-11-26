import { useCallback, useEffect, useRef, useState, type ReactElement } from "react";
import { createFileRoute, redirect, useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useDeleteCurrentSession } from "@/api/generated/amplop";
import type { ApiError } from "@/api/transport";
import { cn } from "@/lib/cn";
import { smallTarget } from "@/components/link-style";
import { SignedOutPage } from "@/components/signed-out-page";
import { Button } from "@/components/ui/button";
import { readSession } from "@/features/auth/guard";
import { clearAccountCache, resetSession } from "@/features/auth/session-cache";
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
  head: () => ({ meta: [{ title: `${setUpName} - amplop` }] }),
});

// TwoFactorSetup is the setup's steps in a card, with a way to sign out
// instead. Finishing, here or elsewhere, opens Security, which says
// two-factor is on.
function TwoFactorSetup(): ReactElement {
  const { session } = Route.useRouteContext();
  const client = useQueryClient();
  const navigate = useNavigate();
  const [canceled, setCanceled] = useState(false);
  // Where the card is going: nowhere yet, into the app once the account is
  // read afresh, signing out, or gone. Sign out, once pressed, wins over a
  // finish seen meanwhile, and the card leaves once, so a later answer
  // cannot send it elsewhere.
  const exit = useRef<"staying" | "entering" | "signing-out" | "left">("staying");
  // Whether the card is still on the page, for answers that come later.
  const mounted = useRef(false);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const toSignIn = useCallback(() => {
    if (exit.current === "left") return;
    exit.current = "left";
    clearAccountCache(client);
    void navigate({ to: "/sign-in", replace: true, ignoreBlocker: true });
  }, [client, navigate]);

  const toSecurity = useCallback(() => {
    if (exit.current !== "staying" && exit.current !== "entering") return;
    exit.current = "left";
    void navigate({
      to: "/settings/security",
      state: { twoFactorChange: "on" },
      replace: true,
      ignoreBlocker: true,
    });
  }, [navigate]);

  // A 401 from any request here: the session ended, e.g. its 15 minutes
  // ran out or another browser signed it out. The session-end rule opens
  // sign-in, which returns here; the card goes nowhere else.
  const onError = useCallback((err: ApiError) => {
    if (err.status === unauthorized) exit.current = "left";
  }, []);
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

  // Turned on elsewhere, e.g. in another tab, or by a finish whose answer
  // was lost. The cached session still says it may only set it up, which
  // would send the app's guard straight back here, so what describes the
  // session is read afresh first. A card gone meanwhile goes nowhere.
  useEffect(() => {
    if (stage !== "done" || exit.current !== "staying") return;
    // Set before the reads, which change the stage and run this again.
    exit.current = "entering";
    void resetSession(client).then(() => {
      if (mounted.current) toSecurity();
    });
  }, [stage, client, toSecurity]);

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
