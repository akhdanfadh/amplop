import { useRef, type ReactElement, type ReactNode } from "react";
import { useFocusOnOpen } from "@/hooks/use-focus-on-open";
import { ConversationList } from "./list";
import { MailPanes } from "./panes";
import type { ViewPlace } from "./places";
import { placeName } from "./text";

// PlacePage is a place's mail: its conversation list beside reader, the
// reading pane. pane says what was opened: the list, with nothing in the
// reading pane, or the reader, e.g. a conversation or Page not found.
export function PlacePage({
  place,
  pane,
  reader,
}: {
  place: ViewPlace;
  pane: "list" | "reader";
  reader: ReactNode;
}): ReactElement {
  const headingRef = useRef<HTMLHeadingElement>(null);
  // Each page focuses its heading when it opens (DESIGN.md "Focus"), so the
  // keyboard starts in the place, not back in the sidebar; what the reading
  // pane shows takes focus itself.
  useFocusOnOpen(headingRef, pane === "list" ? place : null);

  return (
    <MailPanes
      pane={pane}
      list={
        <main className="flex h-full flex-col" aria-labelledby="place-heading">
          <header className="border-b px-4 py-3">
            <h1 ref={headingRef} id="place-heading" tabIndex={-1} className="text-lg font-semibold">
              {placeName[place]}
            </h1>
          </header>
          {/* A new place is a new list: its own scroll position and first
              load. */}
          <ConversationList key={place} place={place} />
        </main>
      }
      reader={reader}
    />
  );
}
