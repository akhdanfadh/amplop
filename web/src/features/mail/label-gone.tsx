import type { ReactElement } from "react";
import { Link } from "@tanstack/react-router";
import { StatePage } from "@/components/route-states";
import { buttonVariants } from "@/components/ui/button";
import { cn } from "@/lib/cn";
import { labelGoneBody, labelGoneTitle, openInbox } from "./text";

// LabelGone replaces a label's page when the member has no such label
// (DESIGN.md "States"): deleted, in amplop or in Gmail, its account
// disconnected, or an old link. Open Inbox leads on to every account's
// Inbox.
export function LabelGone(): ReactElement {
  return (
    <StatePage title={labelGoneTitle}>
      <p>{labelGoneBody}</p>
      <Link
        to="/mail/$place"
        params={{ place: "inbox" }}
        className={cn(buttonVariants({ variant: "outline" }), "w-fit")}
      >
        {openInbox}
      </Link>
    </StatePage>
  );
}
