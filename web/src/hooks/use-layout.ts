import { useSyncExternalStore } from "react";

// Layout is how much of the frame the window fits (DESIGN.md "Panes and
// widths"): "full" from 1280 px, the sidebar beside the panes; "rail" from
// 1024 px, the sidebar as a 56 px rail that expands into the drawer;
// "drawer" from 780 px, the sidebar as a drawer; "phone" below, one pane
// at a time.
type Layout = "full" | "rail" | "drawer" | "phone";

// Widest first: the first query that matches names the layout.
const queries: [Layout, string][] = [
  ["full", "(min-width: 1280px)"],
  ["rail", "(min-width: 1024px)"],
  ["drawer", "(min-width: 780px)"],
];

// readLayout returns the layout the window fits now.
function readLayout(): Layout {
  return queries.find(([, query]) => matchMedia(query).matches)?.[0] ?? "phone";
}

// watchLayout calls onChange when the window crosses a layout's width, and
// returns the function that stops it.
function watchLayout(onChange: () => void): () => void {
  const lists = queries.map(([, query]) => matchMedia(query));
  for (const list of lists) list.addEventListener("change", onChange);
  return () => {
    for (const list of lists) list.removeEventListener("change", onChange);
  };
}

// useLayout returns the layout the window fits now and renders again when
// it changes. Before the page runs in a browser it answers "full".
export function useLayout(): Layout {
  return useSyncExternalStore(watchLayout, readLayout, () => "full");
}
