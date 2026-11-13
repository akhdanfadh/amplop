import { useEffect, useRef, type ReactElement } from "react";
import { Link } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import {
  getGetCurrentSessionQueryKey,
  useGetCurrentSession,
  useRedeemEmailVerification,
} from "@/api/generated/inbok";
import type { CurrentSession } from "@/api/generated/model";
import { Button } from "@/components/ui/button";
import { linkInvalid, redeemErrorMessage } from "./text";
import { lineLink } from "@/components/link-style";

const unauthorized = 401;

// RedeemForm verifies an email address with secret, the text after # in an
// emailed link, when the user selects Verify email; an empty secret shows
// how to get a link instead. It works signed in or out, and reads the
// session only to word the result.
export function RedeemForm({ secret }: { secret: string }): ReactElement {
  const client = useQueryClient();
  const resultRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  // Set when a redeem starts; focus returns to the button if it can retry.
  const refocus = useRef(false);
  // Signed out is a normal state here: a 401 only means no account to show.
  const current = useGetCurrentSession({ query: { networkMode: "always" } });
  const account = current.error?.status === unauthorized ? undefined : current.data?.data;
  const redeem = useRedeemEmailVerification({
    mutation: {
      gcTime: 0,
      // Waiting keeps the result hidden until the session says whether the
      // account is verified.
      onSettled: () => client.invalidateQueries({ queryKey: getGetCurrentSessionQueryKey() }),
    },
  });
  // A refused link stays refused, so it gets no retry.
  const done = redeem.isSuccess || (redeem.error !== null && linkInvalid(redeem.error));

  // The button is disabled while redeeming, and leaves with a final result;
  // focus moves to that result, or back to the button for a retry.
  useEffect(() => {
    if (!refocus.current || redeem.isPending) return;
    refocus.current = false;
    (done ? resultRef.current : buttonRef.current)?.focus();
  });

  if (!secret) {
    // The links depend on the session, so they wait for it.
    if (current.isPending) return <output>Checking your session...</output>;
    if (account?.emailVerified)
      return (
        <div className="flex flex-col gap-4">
          <p>Your email address is verified.</p>
          <HomeLink account={account} />
        </div>
      );
    return (
      <div className="flex flex-col gap-4">
        <p>Open this page from the link in your verification email.</p>
        <AskForLink account={account} />
      </div>
    );
  }

  if (done)
    return (
      <div ref={resultRef} tabIndex={-1} className="flex flex-col gap-4">
        {redeem.error ? (
          <>
            <p>{redeemErrorMessage(redeem.error, account?.emailVerified ?? false)}</p>
            {account?.emailVerified ? (
              <HomeLink account={account} />
            ) : (
              <AskForLink account={account} />
            )}
          </>
        ) : current.isSuccess && !current.data.data.emailVerified ? (
          // Redeeming needs no session, so the link can belong to another account.
          <>
            <p>
              The link verified another account&apos;s email address. The account you&apos;re signed
              in to is still not verified.
            </p>
            <AskForLink account={account} />
          </>
        ) : (
          <>
            <p>Your email address is verified.</p>
            <HomeLink account={account} />
          </>
        )}
      </div>
    );

  return (
    <form
      className="flex flex-col gap-6"
      aria-label="Verify email"
      aria-busy={redeem.isPending}
      onSubmit={(event) => {
        event.preventDefault();
        if (redeem.isPending) return;
        refocus.current = true;
        redeem.mutate({ data: { secret } });
      }}
    >
      <p>
        Select Verify email to finish verifying your email address. If you didn&apos;t ask for this
        link, close this page.
      </p>
      <Button
        ref={buttonRef}
        type="submit"
        className="w-fit whitespace-normal"
        isDisabled={redeem.isPending}
      >
        {redeem.isPending ? "Verifying email..." : "Verify email"}
      </Button>
      {redeem.error && (
        <div className="flex flex-col gap-2" role="alert">
          <p>{redeemErrorMessage(redeem.error, account?.emailVerified ?? false)}</p>
          {redeem.error.requestId && (
            <p className="text-sm text-muted-foreground">
              Request reference: <code className="break-all">{redeem.error.requestId}</code>
            </p>
          )}
        </div>
      )}
    </form>
  );
}

// HomeLink leads into the app: home when signed in, otherwise sign-in.
function HomeLink({ account }: { account: CurrentSession | undefined }): ReactElement {
  if (!account)
    return (
      <Link to="/sign-in" className={lineLink}>
        Sign in
      </Link>
    );
  return (
    <Link to="/" className={lineLink}>
      Go to home
    </Link>
  );
}

// AskForLink leads to Security, where a new link is sent, through sign-in
// when signed out.
function AskForLink({ account }: { account: CurrentSession | undefined }): ReactElement {
  if (!account)
    return (
      <Link to="/sign-in" className={lineLink}>
        Sign in to ask for a new link
      </Link>
    );
  return (
    <Link to="/settings/security" className={lineLink}>
      Ask for a new link in Security
    </Link>
  );
}
