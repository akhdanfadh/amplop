import { createFileRoute, redirect } from "@tanstack/react-router";

// The site's address opens the Inbox, or, for an administrator who must
// set up two-factor authentication first, Security.
export const Route = createFileRoute("/_app/")({
  beforeLoad: ({ context }) => {
    if (context.session.totpRequired) throw redirect({ to: "/security", replace: true });
    throw redirect({ to: "/mail/$place", params: { place: "inbox" }, replace: true });
  },
});
