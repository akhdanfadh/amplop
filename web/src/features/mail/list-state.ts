import { useCallback, useEffect, useState, type RefCallback } from "react";
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
