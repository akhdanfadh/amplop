import type { ReactElement } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { noMessageOpen } from "@/features/outgoing-messages/text";

// The Outbox's reading pane with no message open.
export const Route = createFileRoute("/_app/_mail/mail/outbox/")({ component: NothingOpen });

function NothingOpen(): ReactElement {
  return (
    <div className="flex h-full items-center justify-center p-6 text-sm text-muted-foreground">
      {noMessageOpen}
    </div>
  );
}
