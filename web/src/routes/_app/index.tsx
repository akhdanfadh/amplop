import { createFileRoute, redirect } from "@tanstack/react-router";

// The site's address opens the Inbox.
export const Route = createFileRoute("/_app/")({
  beforeLoad: () => {
    throw redirect({ to: "/mail/$place", params: { place: "inbox" }, replace: true });
  },
});
