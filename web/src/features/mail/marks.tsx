import type { ReactElement, ReactNode } from "react";

// Span is where a search matched a text: length UTF-16 code units from
// start, as a JavaScript string counts them.
export type Span = { start: number; length: number };

// Marked shows text with each of spans marked in the highlight colour
// (DESIGN.md "Search"), as the API placed them, never by matching text in
// the browser: the API knows which words matched, typos and word starts
// included. Spans out of order, overlapping, or past the text's end are
// skipped, so a stale answer can only mark less.
export function Marked({ text, spans }: { text: string; spans: readonly Span[] }): ReactElement {
  const parts: ReactNode[] = [];
  let at = 0;
  for (const { start, length } of spans) {
    const end = start + length;
    if (start < at || length <= 0 || end > text.length) continue;
    if (start > at) parts.push(text.slice(at, start));
    parts.push(
      <mark key={start} className="rounded-xs bg-highlight text-foreground">
        {text.slice(start, end)}
      </mark>,
    );
    at = end;
  }
  if (at < text.length) parts.push(text.slice(at));
  return <>{parts}</>;
}
