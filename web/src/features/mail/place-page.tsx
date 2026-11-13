import { useRef, type ReactElement } from "react";
import { useFocusOnOpen } from "@/hooks/use-focus-on-open";
import type { ViewPlace } from "./places";
import { listPending, placeName } from "./text";

// PlacePage is a place's mail: its conversation list.
export function PlacePage({ place }: { place: ViewPlace }): ReactElement {
  const headingRef = useRef<HTMLHeadingElement>(null);
  // Each page focuses its heading when it opens (DESIGN.md "Focus"), so the
  // keyboard starts in the place, not back in the sidebar.
  useFocusOnOpen(headingRef, place);

  return (
    <main className="flex h-full flex-col" aria-labelledby="place-heading">
      <header className="border-b px-4 py-3">
        <h1 ref={headingRef} id="place-heading" tabIndex={-1} className="text-lg font-semibold">
          {placeName[place]}
        </h1>
      </header>
      <p className="p-4 text-sm text-muted-foreground">{listPending}</p>
    </main>
  );
}
