import { useCallback, useEffect, useLayoutEffect, useRef, useState, type RefCallback } from "react";
import type { Conversation } from "./conversations";
import { rowHeight } from "./row";

// useToday returns the time now, moving on only when the day changes:
// that is all a row's date depends on ("Yesterday", a weekday), so rows
// draw again once a day, not every minute.
export function useToday(): Date {
  const [today, setToday] = useState(() => new Date());
  useEffect(() => {
    const next = new Date(today);
    next.setHours(24, 0, 1, 0);
    const timer = setTimeout(() => setToday(new Date()), next.getTime() - Date.now());
    return () => clearTimeout(timer);
  }, [today]);
  return today;
}

// useRowSize returns size, a row's height in pixels, which follows the
// browser's text size even when the member changes it while the list is
// open, and probe, the ref for an element 1 rem square, whose size changes
// with it.
//
// DESIGN: a ref callback, so the size is watched from whenever the probe
// is drawn: the list draws a placeholder first, without it, and an effect
// run once at mount would find nothing to watch.
export function useRowSize(): { size: number; probe: RefCallback<HTMLElement> } {
  const [size, setSize] = useState(rowHeight);
  const probe = useCallback((element: HTMLElement | null) => {
    if (!element) return undefined;
    // It reports once on observe too, for a size changed while the probe
    // was not drawn.
    const observer = new ResizeObserver(() => setSize(rowHeight()));
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  return { size, probe };
}

// useKeepPlace keeps the first row on screen where it is when rows change
// above it while the member is scrolled down, e.g. a conversation above
// moving to the top with a reply, or leaving the list. It returns record,
// which the list's scroll handler calls with the scrolling box, and
// forget, for a change that moves the list on purpose, e.g. to the top.
//
// DESIGN: rows are placed by the Virtualizer at fixed offsets, so the
// browser's own scroll anchoring cannot help; the first visible row and
// its offset are remembered, and the box is scrolled to keep them.
export function useKeepPlace(
  rows: readonly Conversation[],
  rowSize: number,
): { record: (box: HTMLElement) => void; forget: () => void } {
  const box = useRef<HTMLElement | null>(null);
  const rowsRef = useRef(rows);
  const place = useRef<{ id: string; offset: number } | null>(null);

  const record = useCallback(
    (element: HTMLElement) => {
      box.current = element;
      const top = element.scrollTop;
      const index = Math.floor(top / rowSize);
      const row = rowsRef.current[index];
      place.current = top > 0 && row ? { id: row.id, offset: top - index * rowSize } : null;
    },
    [rowSize],
  );

  const forget = useCallback(() => {
    place.current = null;
  }, []);

  useLayoutEffect(() => {
    rowsRef.current = rows;
    const element = box.current;
    const kept = place.current;
    if (!element || !kept) return;
    const index = rows.findIndex((row) => row.id === kept.id);
    if (index < 0) return;
    const top = index * rowSize + kept.offset;
    if (Math.abs(element.scrollTop - top) >= 1) element.scrollTop = top;
  }, [rows, rowSize]);

  return { record, forget };
}
