import { createFileRoute, redirect } from "@tanstack/react-router";
import { readSession } from "@/features/auth/guard";
import { keptSignedOut } from "@/features/auth/session-end";

// Pages only for visitors without a session: sign-in, password reset,
// registration. A signed-in member goes to their mail instead.
export const Route = createFileRoute("/_auth/_signed-out")({
  beforeLoad: async ({ context }) => {
    // A page kept signed out to copy from still holds the session it read;
    // the member came here to sign in again.
    if (keptSignedOut()) return;
    // A failed check shows the page: signing in reports the server's state
    // itself, and a broken check must not lock anyone out.
    const session = await readSession(context.queryClient).catch(() => undefined);
    if (session) throw redirect({ to: "/", replace: true });
  },
});
