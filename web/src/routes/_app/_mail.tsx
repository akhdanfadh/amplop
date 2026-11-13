import type { ReactElement } from "react";
import { Outlet, createFileRoute } from "@tanstack/react-router";
import { MailFrame } from "@/features/mail/frame";

// The mail frame: the mail sidebar beside a place's panes, the Outbox, or
// a new message.
export const Route = createFileRoute("/_app/_mail")({
  // NOTE: only the loader, which this route lacks, is split off, so the
  // frame comes with the app, and a page still loading as the member moves
  // between pages shows its loading view inside the frame, not over the
  // whole window; the first load is one loading view over the window.
  codeSplitGroupings: [["loader"]],
  component: MailPages,
});

function MailPages(): ReactElement {
  return (
    <MailFrame>
      <Outlet />
    </MailFrame>
  );
}
