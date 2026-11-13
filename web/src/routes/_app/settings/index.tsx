import { createFileRoute, redirect } from "@tanstack/react-router";

// Settings opens on its first page.
export const Route = createFileRoute("/_app/settings/")({
  beforeLoad: () => {
    throw redirect({ to: "/settings/security", replace: true });
  },
});
