import type { ReactElement } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { nothingOpen } from "@/features/mail/text";

// The reading pane with no conversation open.
export const Route = createFileRoute("/_app/_mail/mail/$place/")({ component: NothingOpen });

function NothingOpen(): ReactElement {
  return (
    <div className="flex h-full items-center justify-center p-6 text-sm text-muted-foreground">
      {nothingOpen}
    </div>
  );
}
