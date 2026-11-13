import { createFileRoute, redirect } from "@tanstack/react-router";
import { readSession } from "@/features/auth/guard";

// The signed-in part of inbok: mail and settings. Before any of it draws,
// a visitor without a session goes to sign-in, which brings them back
// here afterwards.
export const Route = createFileRoute("/_app")({
  beforeLoad: async ({ context, location }) => {
    const session = await readSession(context.queryClient);
    if (!session)
      throw redirect({ to: "/sign-in", search: { redirect: location.href }, replace: true });
    return { session };
  },
});
