import { createFileRoute, notFound } from "@tanstack/react-router";

// The settings only administrators have: "This amplop". A member finds no
// page here. An administrator without two-factor authentication never gets
// this far: _app sends them to set it up.
export const Route = createFileRoute("/_app/settings/_admin")({
  beforeLoad: ({ context }) => {
    if (context.session.role !== "admin") throw notFound();
  },
});
