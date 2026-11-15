import { createFileRoute, redirect } from "@tanstack/react-router";
import { getGetSystemEmailQueryOptions } from "@/api/generated/inbok";
import { readSession } from "@/features/auth/guard";

// The signed-in part of inbok: mail and settings. Before any of it draws,
// a visitor without a session goes to sign-in, which brings them back
// here afterwards, and an administrator without two-factor authentication,
// whose session the server lets do nothing else, goes to set it up.
export const Route = createFileRoute("/_app")({
  beforeLoad: async ({ context, location }) => {
    const session = await readSession(context.queryClient);
    if (!session)
      throw redirect({ to: "/sign-in", search: { redirect: location.href }, replace: true });
    if (session.totpRequired) throw redirect({ to: "/two-factor-setup", replace: true });
    return { session };
  },
  // An administrator's setup reminder (SetupReminder) needs system email
  // before the page first draws, or it would appear later and push the
  // page down. It is only a reminder: a failed read leaves it out and the
  // page draws anyway.
  loader: async ({ context }) => {
    const options = getGetSystemEmailQueryOptions();
    // Only the page load's first try: after it, answered or not, the
    // reminder's own query keeps it current, and waiting here could join a
    // reread paused until the connection returns.
    if (
      context.session.role !== "admin" ||
      context.queryClient.getQueryState(options.queryKey) !== undefined
    )
      return;
    // Tried even when the browser reports offline, so it never waits for
    // the connection.
    await context.queryClient
      .query({ ...options, staleTime: Infinity, networkMode: "always" })
      .catch(() => undefined);
  },
});
