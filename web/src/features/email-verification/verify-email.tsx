import { useState, type ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  getGetCurrentSessionQueryKey,
  useGetCurrentSession,
  useRedeemEmailVerification,
} from "@/api/generated/inbok";
import type { ApiError } from "@/api/transport";
import { useAppForm } from "@/components/form/app-form";
import { useFormRequest } from "@/components/form/request";
import { OutcomeLinks, type OutcomeLink } from "@/components/outcome-links";
import { RequestError } from "@/components/request-error";
import { SignedOutPage } from "@/components/signed-out-page";
import { Spinner } from "@/components/ui/spinner";
import { linkInvalid, redeemErrorMessage } from "./text";

const unauthorized = 401;
const title = "Verify your email address";

// VerifyEmail is the page that verifies an email address with secret, the
// text after # in an emailed link, when the visitor selects Verify email;
// an empty secret shows how to get a link instead. It works signed in or
// out, and reads the session only to word the result.
export function VerifyEmail({ secret }: { secret: string }): ReactElement {
  const client = useQueryClient();
  // Signed out is a normal state here: a 401 only means no account to show.
  const current = useGetCurrentSession({ query: { networkMode: "always" } });
  const account = current.error?.status === unauthorized ? undefined : current.data?.data;
  // Only a 401 says no one is signed in; a failed read could hide a session.
  const visitor: Visitor = current.error?.status === unauthorized ? "signedOut" : "maybeSignedIn";
  const redeem = useRedeemEmailVerification({
    mutation: {
      gcTime: 0,
      // Awaited before the answer reaches the form, so the result shows once
      // the session says whether the account is verified.
      onSettled: () => client.invalidateQueries({ queryKey: getGetCurrentSessionQueryKey() }),
    },
  });
  const request = useFormRequest(redeem, []);
  // A redeem that ended the page: verified, or refused for good.
  const [done, setDone] = useState<
    { outcome: "verified" } | { outcome: "refused"; error: ApiError }
  >();
  const form = useAppForm({
    defaultValues: {},
    onSubmit: async ({ value, formApi }) => {
      const sent = await request.send(formApi, value, { data: { secret } });
      if (sent.outcome === "done") setDone({ outcome: "verified" });
      // A refused link stays refused, so it gets no retry.
      if (sent.outcome === "refused" && linkInvalid(sent.error))
        setDone({ outcome: "refused", error: sent.error });
    },
  });
  const verified = account?.emailVerified ?? false;

  if (!secret) {
    // The links depend on the session, so they wait for it.
    if (current.isPending)
      return (
        <SignedOutPage title={title} step="checking">
          {/* Shown after 300 ms, so a quick check shows nothing
            (DESIGN.md "States"). */}
          <output className="flex pending-delay items-center gap-2 text-muted-foreground">
            <Spinner />
            Checking your session...
          </output>
        </SignedOutPage>
      );
    if (verified)
      return (
        <SignedOutPage title="Your email address is verified" step="verified">
          <OutcomeLinks links={[homeLink(visitor)]} />
        </SignedOutPage>
      );
    return (
      <SignedOutPage
        title="Open the link from your email"
        lead="This page needs the link from your verification email."
        step="no-link"
      >
        <OutcomeLinks links={[askForLink(visitor)]} />
      </SignedOutPage>
    );
  }

  if (done?.outcome === "refused")
    return verified ? (
      <SignedOutPage title="Your email address is verified" step="ended">
        <p>{redeemErrorMessage(done.error, verified)}</p>
        <OutcomeLinks links={[homeLink(visitor)]} />
      </SignedOutPage>
    ) : (
      <SignedOutPage title="This link no longer works" step="ended">
        <p>{redeemErrorMessage(done.error, verified)}</p>
        <OutcomeLinks links={[askForLink(visitor)]} />
      </SignedOutPage>
    );

  // Redeeming needs no session, so the link can belong to another account.
  if (done && current.isSuccess && !current.data.data.emailVerified)
    return (
      <SignedOutPage
        title="Another account was verified"
        lead="The account you're signed in to is still not verified."
        step="other"
      >
        <OutcomeLinks links={[askForLink(visitor)]} />
      </SignedOutPage>
    );

  if (done)
    return (
      <SignedOutPage title="Your email address is verified" step="done">
        <OutcomeLinks links={[homeLink(visitor)]} />
      </SignedOutPage>
    );

  return (
    <SignedOutPage title={title} lead="Select Verify email to finish." step="form">
      <p className="text-muted-foreground">
        If you didn&apos;t ask for this link, close this page.
      </p>
      <form.AppForm>
        <form.Form label="Verify email">
          <form.SubmitButton working="Verifying email...">Verify email</form.SubmitButton>
          <RequestError
            errors={request.summary((err) => redeemErrorMessage(err, verified))}
            requestId={request.reference}
          />
        </form.Form>
      </form.AppForm>
    </SignedOutPage>
  );
}

// Who opened the page, as far as the session read tells: "signedOut" after
// a 401, otherwise "maybeSignedIn", also when the read failed.
type Visitor = "signedOut" | "maybeSignedIn";

// homeLink leads into the app for visitor: sign-in when signed out,
// otherwise home.
function homeLink(visitor: Visitor): OutcomeLink {
  return visitor === "signedOut"
    ? { to: "/sign-in", label: "Sign in" }
    : // Home asks a visitor without a session to sign in, so it works after
      // a failed session read too.
      { to: "/", label: "Go to home" };
}

// askForLink leads to Security, where a new link is sent, in words for
// visitor.
function askForLink(visitor: Visitor): OutcomeLink {
  return {
    // Security asks a visitor without a session to sign in first and then
    // opens, so the link works either way; only its words differ.
    to: "/settings/security",
    label:
      visitor === "signedOut" ? "Sign in to ask for a new link" : "Ask for a new link in Security",
  };
}
