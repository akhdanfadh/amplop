import { createFileRoute, notFound, redirect } from "@tanstack/react-router";

// The settings only administrators have: "This inbok". A member finds no
// page here; an administrator without two-factor authentication, which
// the server requires for them, is sent to set it up.
export const Route = createFileRoute("/_app/settings/_admin")({
  beforeLoad: ({ context }) => {
    if (context.session.role !== "admin") throw notFound();
    if (context.session.totpRequired) throw redirect({ to: "/settings/security", replace: true });
  },
});
