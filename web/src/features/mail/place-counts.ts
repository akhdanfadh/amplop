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
