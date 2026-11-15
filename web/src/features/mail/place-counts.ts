import { useQueryClient } from "@tanstack/react-query";
import { useLiveQuery } from "@tanstack/react-db";
import { mailData, type PlaceCount } from "./conversations";
import type { Place } from "./places";

// usePlaceCounts returns every place of the member's accounts with its
// unread count, as last read, and empty before the first read. A count
// changes when mail is read again (refreshMail).
export function usePlaceCounts(): PlaceCount[] {
  const client = useQueryClient();
  return useLiveQuery(mailData(client).placeCounts).data;
}

// unreadIn returns how many conversations of place are unread: in
// accountId's, or in every account's when accountId is undefined.
export function unreadIn(
  counts: readonly PlaceCount[],
  place: Place,
  accountId: number | undefined,
): number {
  return counts
    .filter(
      (count) =>
        count.place === place && (accountId === undefined || count.accountId === accountId),
    )
    .reduce((sum, count) => sum + count.unread, 0);
}

// LabelNode is one label of a tree: its place, its own name, its name
// with its parents' ("Work/Projects"), its unread count, and the labels
// under it.
export type LabelNode = {
  place: string;
  name: string;
  path: string;
  unread: number;
  children: LabelNode[];
};

// Labels sort as a reader expects, Work before work-notes, numbers in
// number order; the API's order is the database's.
const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });

// labelTree returns accountId's labels in counts as a tree, each label
// under its parent, as List Places nests them, and each level in name
// order. A label whose parent counts lack sits at the top.
export function labelTree(counts: readonly PlaceCount[], accountId: number): LabelNode[] {
  const labels = counts.filter((count) => count.accountId === accountId && count.name !== null);
  const nodes = new Map(
    labels.map((label) => [
      label.place,
      {
        place: label.place,
        name: label.name ?? "",
        path: label.name ?? "",
        unread: label.unread,
        children: [] as LabelNode[],
      },
    ]),
  );
  const top: LabelNode[] = [];
  for (const label of labels) {
    const node = nodes.get(label.place);
    if (!node) continue;
    const parent = label.parent === null ? undefined : nodes.get(label.parent);
    (parent ? parent.children : top).push(node);
  }
  sortLevel(top);
  setPaths(top, "");
  return top;
}

// setPaths names each label of level and below with its parents', under
// parent's path.
function setPaths(level: LabelNode[], parent: string): void {
  for (const node of level) {
    node.path = parent ? `${parent}/${node.name}` : node.name;
    setPaths(node.children, node.path);
  }
}

// sortLevel orders level and every level under it by name.
function sortLevel(level: LabelNode[]): void {
  level.sort((a, b) => collator.compare(a.name, b.name));
  for (const node of level) sortLevel(node.children);
}

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
