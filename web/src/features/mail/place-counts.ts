import type { PlaceCount } from "./conversations";

// labelPath returns the names of place's label and its parents, the top
// one first, or none when counts lack it.
export function labelPath(counts: readonly PlaceCount[], place: string): string[] {
  const byPlace = new Map(counts.filter((c) => c.name !== null).map((c) => [c.place, c]));
  const names: string[] = [];
  const seen = new Set<string>();
  for (
    let label = byPlace.get(place);
    label;
    label = label.parent ? byPlace.get(label.parent) : undefined
  ) {
    // A loop could come only from a server fault; it ends the path.
    if (seen.has(label.place)) break;
    seen.add(label.place);
    names.unshift(label.name ?? "");
  }
  return names;
}
