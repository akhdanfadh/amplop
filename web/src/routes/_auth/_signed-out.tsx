import { createFileRoute, redirect } from "@tanstack/react-router";
import { readSession } from "@/features/auth/guard";

// Pages only for visitors without a session: sign-in, password reset,
// registration. A signed-in member goes to their mail instead.
export const Route = createFileRoute("/_auth/_signed-out")({
  beforeLoad: async ({ context }) => {
    // A failed check shows the page: signing in reports the server's state
    // itself, and a broken check must not lock anyone out.
    const session = await readSession(context.queryClient).catch(() => undefined);
    if (session) throw redirect({ to: "/", replace: true });
  },
});
