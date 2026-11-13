import { createFileRoute, redirect } from "@tanstack/react-router";

// /security is an entry address: the server links it in emails, e.g. the
// recovery notice, so it stays and opens Security wherever settings put it.
export const Route = createFileRoute("/security")({
  beforeLoad: () => {
    throw redirect({ to: "/settings/security", replace: true });
  },
});
