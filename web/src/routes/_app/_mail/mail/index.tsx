import { createFileRoute, redirect } from "@tanstack/react-router";

// Mail opens on the Inbox.
export const Route = createFileRoute("/_app/_mail/mail/")({
  beforeLoad: () => {
    throw redirect({ to: "/mail/$place", params: { place: "inbox" }, replace: true });
  },
});
