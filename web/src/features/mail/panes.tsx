import type { ReactElement, ReactNode } from "react";
import { cn } from "@/lib/cn";
import { StateRegion } from "@/components/route-states";
import { useLayout } from "@/hooks/use-layout";

// MailPanes shows list beside reader, the conversation list and the
// reading pane (DESIGN.md "Panes and widths"). The list grows with the
// window from 300 px to 440 px, so the reading pane keeps at least 480 px
// down to 780 px, where one pane shows at a time: pane names which.
//
// DESIGN: no divider to drag: the fluid width covers what a member would
// drag for, and a divider needs a way to resize without dragging
// (WCAG 2.5.7), a handle visible at rest, and its width in pixels.
export function MailPanes({
  list,
  reader,
  pane,
}: {
  list: ReactNode;
  reader: ReactNode;
  pane: "list" | "reader";
}): ReactElement {
  const layout = useLayout();
  const phone = layout === "phone";

  // One tree at every width, so a resize keeps the list, and focus in it,
  // where it is; on a phone the list hides while the reader shows.
  return (
    <div className="flex min-h-0 flex-1">
      <div
        className={cn(
          "overflow-auto",
          phone ? "min-w-0 flex-1" : "w-[clamp(300px,26vw,440px)] shrink-0 border-r",
          phone && pane === "reader" && "hidden",
        )}
      >
        {list}
      </div>
      {(!phone || pane === "reader") && (
        <div className="min-w-0 flex-1 overflow-auto">
          {/* A state here, e.g. a conversation not found, sits in the page
              whose main the list holds; on a phone the reader is the page. */}
          <StateRegion value={phone ? "page" : "pane"}>{reader}</StateRegion>
        </div>
      )}
    </div>
  );
}
