import { createFileRoute, redirect } from "@tanstack/react-router";

// /compose opens the compose window over the Inbox (DESIGN.md "URLs"): an
// entry point only, e.g. typed or bookmarked, which history never keeps,
// so Back can't open the window again. The window lives in no address.
export const Route = createFileRoute("/_app/_mail/compose")({
  beforeLoad: ({ context }) => {
    context.compose.open();
    throw redirect({ to: "/mail/$place", params: { place: "inbox" }, replace: true });
  },
});
